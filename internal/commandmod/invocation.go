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
// The value is the flags that consume the following word, which have to be
// known to find where the wrapped command starts — in `sudo -u root npm
// publish`, `root` is sudo's argument, not the program.
var wrappers = map[string]map[string]bool{
	"sudo":    {"-u": true, "--user": true, "-g": true, "--group": true, "-C": true, "-p": true, "--prompt": true, "-h": true, "--host": true, "-D": true, "--chdir": true, "-R": true},
	"doas":    {"-u": true, "-C": true},
	"env":     {"-u": true, "--unset": true, "-C": true, "--chdir": true, "-S": true, "--split-string": true},
	"xargs":   {"-a": true, "--arg-file": true, "-d": true, "--delimiter": true, "-E": true, "-I": true, "-i": true, "--replace": true, "-L": true, "-l": true, "-n": true, "--max-args": true, "-P": true, "--max-procs": true, "-s": true, "--max-chars": true},
	"nohup":   {},
	"nice":    {"-n": true, "--adjustment": true},
	"ionice":  {"-c": true, "-n": true, "-p": true},
	"stdbuf":  {"-i": true, "-o": true, "-e": true},
	"timeout": {"-k": true, "--kill-after": true, "-s": true, "--signal": true},
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
	if len(argv) == 0 {
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
	takesValue, isWrapper := wrappers[basename(argv[0])]
	if !isWrapper {
		return nil
	}

	for i := 1; i < len(argv); i++ {
		arg := argv[i]

		// `--` ends the wrapper's own options; whatever follows is the command.
		if arg == "--" {
			return rest(argv, i+1)
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			// An `env` assignment prefix — `env FOO=1 npm publish` — is
			// env's argument, not the program. Skip past it the way env does.
			if strings.Contains(arg, "=") && !strings.HasPrefix(arg, "=") {
				continue
			}
			return rest(argv, i)
		}
		// A flag written `--user=root` carries its value inline; one listed in
		// takesValue eats the next word.
		if !strings.Contains(arg, "=") && takesValue[arg] {
			i++
		}
	}
	// A wrapper with no command after it — bare `sudo`, or `xargs` reading
	// its program from a default. Nothing further can be seen.
	return nil
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
		if name == "" {
			continue
		}

		value := ""
		if eq := strings.Index(name, "="); eq >= 0 {
			value, name = name[eq+1:], name[:eq]
		}
		if name == "" {
			continue
		}
		// Last occurrence wins, which is what most programs do with a repeated
		// flag, and matters only when one is repeated with different values.
		flags[name] = value
	}
	return flags
}
