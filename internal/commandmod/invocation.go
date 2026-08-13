package commandmod

import (
	"path"
	"strings"
)

// wrappers are programs whose own arguments name another program to run.
//
// A rule about npm is defeated by `sudo npm publish` if only the wrapper is
// reported, and a rule about sudo is defeated by omitting it. Both are emitted:
// the wrapper because it is genuinely being invoked, and what it wraps because
// that is what will actually do the thing.
//
// wrapper is what has to be known about one wrapper to find where the command
// it wraps begins.
//
// Two things, because a wrapper's own arguments come in exactly two shapes that
// the wrapped program has to be told apart from:
//
//	takesValue  a flag that consumes the following word — in `sudo -u root npm
//	            publish`, `root` is sudo's argument, not the program.
//	positionals a mandatory bare word BEFORE the command, taken by no flag. In
//	            `timeout 5 npm publish`, `5` is the duration. Without this the
//	            first non-flag word is read as the program, which both reports a
//	            binary named `5` that nothing invokes and loses npm entirely.
//
// A count rather than a predicate on the word. Whether `5` looks like a
// duration is not the question — `timeout` takes one positional wherever it
// appears and whatever it is spelled like, and a shape test would have to guess
// about words like `infinity` or `0.5s` and would still be a guess.
type wrapper struct {
	takesValue map[string]bool
	// positionals is how many bare words the wrapper consumes before the
	// program. Zero for every wrapper that names the command first, which is
	// most of them.
	positionals int
}

// wrappers are programs whose own arguments name another program to run.
//
// A rule about npm is defeated by `sudo npm publish` if only the wrapper is
// reported, and a rule about sudo is defeated by omitting it. Both are emitted:
// the wrapper because it is genuinely being invoked, and what it wraps because
// that is what will actually do the thing.
var wrappers = map[string]wrapper{
	"sudo":   {takesValue: map[string]bool{"-u": true, "--user": true, "-g": true, "--group": true, "-C": true, "-p": true, "--prompt": true, "-h": true, "--host": true, "-D": true, "--chdir": true, "-R": true}},
	"doas":   {takesValue: map[string]bool{"-u": true, "-C": true}},
	"env":    {takesValue: map[string]bool{"-u": true, "--unset": true, "-C": true, "--chdir": true, "-S": true, "--split-string": true}},
	"xargs":  {takesValue: map[string]bool{"-a": true, "--arg-file": true, "-d": true, "--delimiter": true, "-E": true, "-I": true, "-i": true, "--replace": true, "-L": true, "-l": true, "-n": true, "--max-args": true, "-P": true, "--max-procs": true, "-s": true, "--max-chars": true}},
	"nohup":  {},
	"ionice": {takesValue: map[string]bool{"-c": true, "-n": true, "-p": true}},
	"stdbuf": {takesValue: map[string]bool{"-i": true, "-o": true, "-e": true}},

	// `nice` takes its increment ONLY behind a flag — the synopsis is
	// `nice [-n increment] utility`. So `nice 10 npm publish` really would try
	// to run a program called `10`, and reporting that is correct rather than a
	// fabrication. No positional here.
	"nice": {takesValue: map[string]bool{"-n": true, "--adjustment": true}},

	// `timeout` is the one wrapper whose synopsis puts a mandatory bare word
	// before the command: `timeout [OPTION] DURATION COMMAND [ARG]...`.
	"timeout": {takesValue: map[string]bool{"-k": true, "--kill-after": true, "-s": true, "--signal": true}, positionals: 1},
}

// fromArgv turns one resolved argument vector into the invocations it performs
// — the vector's own program, plus whatever it wraps.
//
// Never assumes argv[0] is the binary in the sense of trusting it blindly: an
// unresolved word can expand to nothing and shift the whole vector, so
// `$UNSET npm publish` arrives as `[npm publish]` and `npm` legitimately is
// the program, while `$UNSET` alone arrives empty and is dropped before it
// gets here. What this does assume is only that a vector which survived
// expansion non-empty has its program first, which is what a shell assumes too.
func fromArgv(argv []string) []Invocation {
	if len(argv) == 0 || basename(argv[0]) == "" {
		// An empty program name is not a program. Reached here when a word
		// expanded to an empty string, or when a wrapper's own arguments run
		// out — either way there is nothing to name, and an invocation with an
		// empty bin would reach a matcher as a program no rule can mean.
		return nil
	}

	invs := []Invocation{newInvocation(argv)}

	if nested := unwrap(argv); len(nested) > 0 {
		// Recursive, because wrappers stack: `sudo nohup npm publish` is two
		// wrappers deep, and stopping at the first would report sudo and nohup
		// while missing npm.
		invs = append(invs, fromArgv(nested)...)
	}
	return invs
}

// unwrap returns the vector a wrapper is wrapping, or nil if this is not a
// wrapper or names nothing to run.
func unwrap(argv []string) []string {
	w, isWrapper := wrappers[basename(argv[0])]
	if !isWrapper {
		return nil
	}

	// How many of the wrapper's own bare words are still to come before the
	// program. Counted down as they are passed, so `timeout 5 npm publish`
	// spends the one on `5` and reports npm.
	positionals := w.positionals

	for i := 1; i < len(argv); i++ {
		arg := argv[i]

		// `--` ends the wrapper's own options; whatever follows is the command.
		// Its positionals still come first — `timeout -- 5 npm` is the duration
		// then the program, since `--` ends options, not arguments.
		if arg == "--" {
			return afterPositionals(argv, i+1, positionals)
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			// An `env` assignment prefix — `env FOO=1 npm publish` — is
			// env's argument, not the program. Skip past it the way env does.
			// A word STARTING with `=` has no variable name in front of the
			// equals, so env does not take it as an assignment and neither do we.
			if strings.Contains(arg, "=") && !strings.HasPrefix(arg, "=") {
				continue
			}
			// A bare word the wrapper itself consumes — timeout's duration.
			if positionals > 0 {
				positionals--
				continue
			}
			return rest(argv, i)
		}
		if w.consumesNextWord(arg) {
			i++
		}
	}
	// A wrapper with no command after it — bare `sudo`, or `xargs` reading
	// its program from a default. Nothing further can be seen.
	return nil
}

// consumesNextWord reports whether this flag eats the word after it.
//
// Only the SEPARATED spelling does, and a plain lookup is the whole test —
// because the other two spellings are not keys in the table:
//
//	--user root   separated — listed in takesValue, so the next word is eaten
//	--user=root   inline    — the value is already here. No key contains `=`,
//	                          so the lookup declines, which is correct.
//	-uroot        clustered — a short flag carrying its value with no space.
//	                          The whole word is not a key either, so the lookup
//	                          declines. `sudo -uroot npm publish` reports npm,
//	                          and `sudo -unpm publish` reports publish — run as
//	                          user npm.
//
// An explicit `strings.Contains(arg, "=")` guard was tried here and proven
// unobservable: it can only change the answer for a word that is both
// `=`-bearing AND a takesValue key, and no key in the table contains an `=`.
// Left out rather than kept as a branch a reader would take as load-bearing.
func (w wrapper) consumesNextWord(arg string) bool {
	return w.takesValue[arg]
}

// afterPositionals returns the vector starting past the wrapper's own remaining
// bare words. Used after a `--`, where options have ended but the wrapper's
// positional arguments have not.
func afterPositionals(argv []string, from, positionals int) []string {
	return rest(argv, from+positionals)
}

func rest(argv []string, from int) []string {
	if from >= len(argv) {
		return nil
	}
	return argv[from:]
}

// newInvocation builds one invocation from a resolved vector.
func newInvocation(argv []string) Invocation {
	return Invocation{
		Bin:   basename(argv[0]),
		Argv:  argv,
		Flags: parseFlags(argv[1:]),
	}
}

// basename reduces a program to the name a rule is written against: `npm`
// rather than `/usr/local/bin/npm`, so a rule need not enumerate every path a
// program can live at and an agent cannot evade one by spelling the path out.
func basename(prog string) string {
	if prog == "" {
		return ""
	}
	// Normalised so a Windows-style path in a command line does not read as
	// one long basename.
	prog = strings.ReplaceAll(prog, `\`, "/")
	return path.Base(prog)
}

// parseFlags pulls the flags out of an argument vector, keyed without their
// leading dashes.
//
// A flag given without a value carries an empty string, so a rule can ask
// whether a flag is present without knowing whether that flag takes one. Only
// the inline forms — `--tag=next` and `-m=x` — are read as carrying a value:
// whether a separated `--tag next` is a value or the next positional argument
// depends on the program's own option table, which is not knowable here, and
// guessing would report a value the command may not have.
func parseFlags(args []string) map[string]string {
	flags := map[string]string{}
	for _, arg := range args {
		if len(arg) < 2 || !strings.HasPrefix(arg, "-") {
			continue
		}
		if arg == "--" {
			// Everything after is positional by definition.
			break
		}

		name := strings.TrimLeft(arg, "-")

		value := ""
		if eq := strings.Index(name, "="); eq >= 0 {
			value, name = name[eq+1:], name[:eq]
		}
		// An all-dashes word — `---`, or `-=x` whose name is empty once the
		// value is split off. A flag keyed by the empty string would be
		// reachable as `"" in .flags`, which is a match no rule means.
		//
		// One guard, not two. A check before the `=` split was proven
		// unreachable: `-` and `--` leave the loop earlier (the length test and
		// the `--` break), and every other all-dash word reaches here with an
		// empty name anyway. Verified by removing it and confirming
		// parseFlags's output is unchanged across every dash spelling.
		if name == "" {
			continue
		}
		// Last occurrence wins, which is what most programs do with a repeated
		// flag, and matters only when one is repeated with different values.
		flags[name] = value
	}
	return flags
}
