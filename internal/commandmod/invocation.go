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
	// suppressedBy are flags that make the wrapper DESCRIBE its argument
	// instead of running it. `command -v npm` prints npm's path and runs
	// nothing, so reporting npm there is a rule firing on a line that does not
	// invoke it — the over-reporting direction the module calls the worse one.
	//
	// A third concept only because the second-worst outcome is already ruled
	// out by the other two: takesValue would wrongly eat the program word, and
	// a positional would wrongly eat it too. Neither can express "there is no
	// invocation here at all", which is what these flags mean. Empty for every
	// wrapper that has no such flag, which is all of them but `command`.
	suppressedBy map[string]bool
	// wrapsNothing marks a wrapper whose bare words are never a program.
	//
	// `su` and `runuser` name a USER, and the command they run comes only from
	// `-c`. Without this the first bare word is reported as a binary, so
	// `su someuser` invents a program called someuser — an argument reported as
	// a program, which the module calls the one outcome worse than missing it.
	//
	// Distinct from simply leaving them out of the table: they ARE unwrapped,
	// just on the interpreter path, and the entry is what stops the wrapper
	// path reporting a user name on the way.
	wrapsNothing bool
	// requiresFlag is wrapsNothing conditioned on a flag, for a wrapper with
	// two synopses that disagree about what a bare word is.
	//
	// `runuser` is the only one: with `-u user` the bare word is the program,
	// without it the bare word is a user. When this is non-empty the wrapper
	// wraps a program only if one of these flags was seen. Nil for every
	// wrapper whose bare word means one thing.
	requiresFlag map[string]bool
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

	// The rest were named by the unwrap-interpreter-payloads task. Each runs
	// the program named after it, so each was defeating a rule about that
	// program by reporting only itself. They are ordinary wrappers — they hand
	// on a VECTOR, not a string — so none of them needs the literalness test or
	// the depth bound that interpreter payloads do.

	// `setsid [-w] program [args]`. Its options are all boolean.
	"setsid": {},
	// `unbuffer [-p] program [args]`, from expect. `-p` is boolean.
	"unbuffer": {},
	// `watch [OPTION] command`. `-n`/`--interval` and `-d=` take a value; the
	// command is the first bare word after them.
	"watch": {takesValue: map[string]bool{"-n": true, "--interval": true, "-d": true, "--differences": true}},
	// `torify program [args]` and `proxychains program [args]`. proxychains
	// takes `-f conf` before the program.
	"torify":      {},
	"proxychains": {takesValue: map[string]bool{"-f": true}},

	// `flock [options] file|dir command [args]` — a mandatory bare word (the
	// lock path) before the command, the same shape as timeout's duration.
	//
	// The `-c` spelling, `flock file -c "npm publish"`, is a command STRING and
	// is handled on the interpreter path — flock is in the interpreters table
	// too. Here `-c` is declared value-taking so the scan does NOT return the
	// payload as a vector: without that it reports one program whose name is
	// the whole string `npm publish`, a binary nothing invokes and a basename
	// no rule can match.
	"flock": {takesValue: map[string]bool{"-w": true, "--wait": true, "--timeout": true, "-E": true, "--conflict-exit-code": true, "-c": true, "--command": true}, positionals: 1},

	// `chroot NEWROOT [COMMAND [ARG]...]` — the new root is a mandatory bare
	// word before the command.
	"chroot": {takesValue: map[string]bool{"--userspec": true, "--groups": true}, positionals: 1},

	// `runuser` has two synopses that disagree about what a bare word is:
	//
	//	runuser -u user [--] COMMAND [args]   the bare word is the PROGRAM
	//	runuser [-] [user [args]]             the bare word is the USER
	//
	// So the meaning of a bare word depends on whether `-u` was given. That is
	// resolved by reading the flag, which the scan already does: `-u` takes a
	// value, so `runuser -u x npm publish` spends `x` on the flag and reports
	// npm — correct for the first synopsis. Under the second, `runuser someuser`
	// would report someuser as a program, which is the fabrication the module
	// refuses, so wrapsNothing is NOT set here and the two are told apart by
	// requiresFlag instead.
	"runuser": {requiresFlag: map[string]bool{"-u": true, "--user": true}, takesValue: map[string]bool{"-u": true, "--user": true, "-g": true, "--group": true, "-G": true, "--supp-group": true, "-s": true, "--shell": true, "-c": true, "--command": true}},

	// `su [options] [-] [user [args]]`. su never takes a program as a bare
	// word: the bare word is the USER, and the command it runs comes only from
	// `-c`. So this entry exists purely to STOP the scan from promoting a user
	// name to a program — `su someuser` must not report a binary called
	// someuser, and `su -c "npm publish" someuser` must not either.
	//
	// wrapsNothing is what says that. A positional count cannot: it would
	// consume the user and then report the NEXT bare word as a program, which
	// for `su user -x` is su's argument to the user's shell, not a program.
	//
	// The command string is handled on the interpreter path instead, where the
	// literalness test lives — su is in the interpreters table for that.
	"su": {wrapsNothing: true, takesValue: map[string]bool{"-c": true, "--command": true, "-s": true, "--shell": true, "-g": true, "--group": true, "-G": true, "--supp-group": true, "--session-command": true}},

	// `command`, `exec` and `builtin` are shell BUILTINS rather than programs,
	// which the task flagged as a separate judgement. They are in, and being a
	// builtin is the reason rather than an objection.
	//
	// What this module reports is what is about to run. `exec npm publish`
	// replaces the shell with npm, so npm runs — more definitely than under
	// sudo, which can still refuse. `command npm publish` runs npm bypassing
	// functions and aliases, which is precisely the spelling someone reaches
	// for to get the real npm. `builtin` is the odd one and is in for
	// consistency of the failure direction: `builtin npm publish` runs no npm
	// because npm is not a builtin, so reporting npm is a rule firing on a line
	// that does nothing — but the same is already true of `sudo notaprogram`,
	// and the module's answer everywhere else is that whether the program
	// EXISTS is not a question a static reader can answer. Over-reporting a
	// command that fails is the same class as reporting `nice 10` as a program,
	// which TestNesting_NiceTakesNoBarePositional settles as correct.
	//
	// That the shell resolves them without an exec is invisible here and does
	// not change what a rule needs to know: the word after them is a program
	// the line is trying to run.
	//
	// `command -v npm` is the case that would over-report — it prints npm's
	// path and runs nothing — so `-v` and `-V` suppress the invocation rather
	// than being skipped as ordinary boolean flags. Verified against a real
	// shell: `command -v echo` prints `echo` and echoes nothing.
	"command": {suppressedBy: map[string]bool{"-v": true, "-V": true}},
	// `exec -a name program` renames the program; the name is exec's argument,
	// not the thing being run.
	"exec":    {takesValue: map[string]bool{"-a": true}},
	"builtin": {},
}

// infixWrappers are programs that name a command in the MIDDLE of their own
// argument vector, delimited by a flag on one side and a terminator on the
// other.
//
// A separate table from `wrappers` because the shape cannot be expressed there
// at all. Every entry in `wrappers` names its command as a SUFFIX — everything
// from some index to the end — and the whole of `unwrap` is a scan for that
// index. `find . -name '*.md' -exec npm publish \; -print` has find's own
// operands on BOTH sides of the command, so there is no index whose suffix is
// the command, and no amount of positional counting produces one.
//
// Two further differences that a row in `wrappers` could not carry either:
//
//   - There can be SEVERAL in one vector. `find . -exec a \; -exec b \;` runs
//     two programs and both are about to run, so both are reported. `unwrap`
//     returns one vector by construction; this returns a list.
//   - Absence of the terminator is a USAGE ERROR rather than a shorter command.
//     `find . -exec npm publish` is rejected by find, which runs nothing at all
//     — so the honest answer is to report nothing from it, not to guess that the
//     rest of the line was meant.
//
// The delimiter arrives as a bare `;`. The shell has already eaten the
// backslash in `\;` by the time this module sees the word, so the match is on
// `;` — verified by the parse, not assumed.
type infixWrapper struct {
	// introducers are the flags after which a command vector begins.
	introducers map[string]bool
	// terminators are the words that end it. `;` runs the command once per
	// match; `+` batches the matches into one run. Which of the two is used
	// changes how many times the program runs, not WHETHER it runs, and a rule
	// asks the second question.
	terminators map[string]bool
}

var infixWrappers = map[string]infixWrapper{
	// `find [path...] [expression]`, where `-exec`/`-execdir`/`-ok`/`-okdir`
	// each introduce a command run against the matched paths.
	//
	// All four are in, and the two prompting forms are in for the same reason
	// the non-prompting ones are. `-ok` asks the user before each run, so it is
	// nearer `sudo` — which can also refuse — than to `command -v`, which
	// describes its argument and runs nothing. A guardrail that stayed silent on
	// `find . -ok npm publish \;` would be silent on a line whose entire purpose
	// is to run npm, on the theory that a human might say no.
	//
	// `-execdir`/`-okdir` differ only in the working directory the command runs
	// in, which is not something a rule about WHICH program runs can care about.
	"find": {
		introducers: map[string]bool{
			"-exec": true, "-execdir": true, "-ok": true, "-okdir": true,
		},
		terminators: map[string]bool{";": true, "+": true},
	},
}

// findPlaceholder is find's placeholder for a matched path.
//
// Dropped from the reported vector rather than carried, and this is the one
// judgement in the infix path that is not forced by find's grammar.
//
// It is an argument the command genuinely receives, which argues for keeping
// it. But WHAT it receives is not `{}` — find replaces it, at runtime, with a
// path chosen by a directory walk. `find . -name '*.md' -exec rm {} \;` deletes
// whatever the tree happens to hold, and this package must not read the tree:
// `commandmod` is a pure function of a string, which is precisely what makes it
// testable and what stops a guardrail's verdict depending on the working
// directory it ran in.
//
// So the choice is between reporting `{}` as written and reporting nothing for
// that operand, and `hasGlob` already settles the identical question one file
// over. `rm *.md` survives expansion with the `*` intact and LOOKS literal —
// isLiteral agrees, a `*` being a plain Lit — yet it names no file, because the
// shell will expand it against a tree nobody here has looked at. It is dropped.
// `{}` is the same word in the same position with the same defect: a token
// standing for a set of paths that only a filesystem walk can name. Reporting
// it would put a path in a file event that no rule can mean and no file can
// match — harmless today only because nothing is named `{}`, which is luck.
//
// The comparison the task file draws — to a process substitution's
// `/dev/fd/63`, which IS kept — is the case that does not apply. That path is
// what the program actually receives and can actually open; the shape of the
// vector is preserved by a value that is true. `{}` is not what the program
// receives. Keeping it would preserve the shape with a value that is false.
//
// What survives is what is knowable: the INVOCATION. `rm` is about to run, with
// certainty, and a rule about running rm fires. Which files it runs against is
// the unknowable tier — stated rather than hidden, and the same answer this
// package gives for `rm *.md` today.
const findPlaceholder = "{}"

// stripPlaceholders drops find's `{}` operands from a command vector.
//
// Only the whole-word spelling. `-exec sh -c 'rm pre{}post' \;` substitutes
// inside the word too, but a word merely CONTAINING `{}` is one whose value is
// partly known, and dropping it would lose more than it saves: the drop exists
// to stop a path nobody can name being reported AS a path, and `pre{}post` is
// not going to be read as a path by anything. It is left as written, the same
// way `rm ./a*.md` keeps its literal prefix.
func stripPlaceholders(argv []word) []word {
	out := make([]word, 0, len(argv))
	for _, w := range argv {
		if w.value == findPlaceholder {
			continue
		}
		out = append(out, w)
	}
	return out
}

// unwrapInfix returns every command vector named in the middle of this vector
// by an infix wrapper, in the order they appear.
//
// Nil for everything that is not an infix wrapper, and for an infix wrapper
// with no COMPLETE clause. A clause is complete only when its terminator is
// present: `find . -exec npm publish` is a line find REJECTS, so reporting npm
// off it would be a rule firing on a command line that runs nothing — the
// over-reporting direction this module calls the worse one.
//
// A complete but EMPTY clause is returned as an empty vector rather than
// skipped, and is declined by the callers — see where it is appended below.
func unwrapInfix(argv []word) [][]word {
	in, ok := infixWrappers[basename(argv[0].value)]
	if !ok {
		return nil
	}

	var out [][]word
	for i := 1; i < len(argv); i++ {
		if !in.introducers[argv[i].value] {
			continue
		}
		// The clause is the words between the introducer and the FIRST
		// terminator after it. Scanned forward rather than to the end of the
		// vector, because find's own operands continue after it.
		//
		// First rather than last, and a word that looks like another introducer
		// in between is simply an argument to this command. Measured against
		// real find: `find . -exec echo ONE -exec echo TWO \;` prints
		// `ONE -exec echo TWO` — one run of echo with four arguments, not two
		// clauses. So the same rule that finds the ordinary case gets this one
		// right too, and pinning it is what stops a later "smarter" scan
		// reporting a second program the line never runs.
		start := i + 1
		end := -1
		for j := start; j < len(argv); j++ {
			if in.terminators[argv[j].value] {
				end = j
				break
			}
		}
		if end < 0 {
			// No terminator anywhere after this introducer. find rejects the
			// whole expression — verified: `find . -name '*.txt' -exec echo X`
			// answers `Expected '... ;' or '... {} +'` and runs nothing at all.
			//
			// So nothing is reported from it, rather than the rest of the vector
			// being read as the command.
			//
			// `break` rather than `continue`, and the two are EQUIVALENT rather
			// than one being right — recorded so the survivor is read as an
			// equivalence and not as this branch being untested. There is no
			// terminator anywhere after this introducer, so a later introducer's
			// own forward scan finds none either and re-derives the same
			// nothing. Measured: replacing this with `continue` leaves the whole
			// suite green. `break` is kept because it says what is true — the
			// rest of the vector holds no complete clause — and stops rather
			// than re-scanning to learn it again.
			break
		}
		// An empty clause — `find . -exec \;`, or `find . -exec {} \;` whose
		// only word is the dropped placeholder — is appended like any other and
		// declined one step later. Both consumers reject a vector with no
		// program on their first line: fromArgv tests `basename(argv[0])` and
		// targetsForArgv the same, so an empty vector names nothing either way.
		//
		// A `len(cmd) > 0` guard was tried here and PROVEN unobservable — the
		// suite stays green with it removed, including the cases that assert
		// `find . -exec {} \;` reports find alone and produces no empty bin.
		// Left out rather than kept as a branch a reader would take as
		// load-bearing.
		out = append(out, stripPlaceholders(argv[start:end]))
		// Resume AT the terminator; the loop's own increment steps past it. A
		// second clause in the same vector is found by the same scan, which is
		// what makes `find . -exec a \; -exec b \;` report both.
		i = end
	}
	return out
}

// word is one resolved argument plus whether it was certain.
//
// A plain []string was enough while unwrapping only ever SLICED the vector: a
// wrapper hands on words that were already there, so nothing new is asserted
// about any of them. Re-parsing an interpreter payload is different in kind —
// it turns one word's TEXT into programs — and that is only sound when the
// text is exactly what was typed. By the time a payload reaches unwrap the AST
// is gone and the two cases are the same string:
//
//	sh -c "npm publish"        -> "npm publish"   certain
//	sh -c "np${X}m publish"    -> "npm publish"   a guess, from an empty env
//
// So literalness rides along on the word instead of being recomputed from the
// value, which is impossible, or re-derived by a second parse of the text,
// which would be the second notion of certainty this module refuses to keep.
type word struct {
	value string
	// literal is isLiteral's answer for the source word, carried down from
	// resolve. Only the payload branch reads it; slicing a vector preserves it
	// positionally for free, which is why it lives on the element rather than
	// in a parallel slice that every slice expression would have to re-align.
	literal bool
}

// values drops the provenance, for the places that want the vector a rule
// reads — Argv on an invocation, and the flag parse.
func values(argv []word) []string {
	out := make([]string, 0, len(argv))
	for _, w := range argv {
		out = append(out, w.value)
	}
	return out
}

// maxUnwrapDepth bounds how many interpreter payloads deep this will re-parse.
//
// The bound exists because payloads nest without limit and each level is a
// fresh parse of attacker-shaped text: `sh -c 'sh -c "sh -c ..."'` is a string
// an agent can write as long as it likes, and the work per level is not
// constant — each parse walks the whole remaining text, so unbounded descent is
// quadratic in a string somebody else chose the length of. The recover in walk
// catches a panic but not a hang, and the task is explicit that a guardrail
// which hangs is as bad as one that crashes.
//
// Four, on what the levels are actually for. One is the ordinary case an agent
// writes (`bash -lc '...'`). Two is a harness wrapping an agent's own `sh -c`
// in its own `bash -lc`, which is the case the task exists for. Three leaves
// room for one more layer nobody planned. Four is past every real form and is
// where a line stops being a command and starts being a nesting exercise.
//
// Going deeper is not more correct in any way a rule benefits from: the value
// of unwrapping is that a rule about npm fires on the line that runs npm, and
// a payload buried five interpreters down is not a thing an agent writes by
// accident. At the limit what was found is returned rather than nothing —
// every interpreter on the way down is still reported, so a rule about `sh`
// fires at any depth, and only the innermost payload's contents are unread.
//
// Depth counts payload re-entries ONLY. Wrapper unwrapping (`sudo`, `xargs`)
// is not bounded by it and must not be: that recursion consumes words from a
// vector of finite length, so it terminates on its own, and TestNesting_
// DeepStructureIsFlatCompletely pins 100 stacked sudos being reported in full.
const maxUnwrapDepth = 4

// interpreters are programs that take a command string as an argument and run
// it as a script.
//
// Distinct from the wrappers table because what they name is not a vector, it
// is TEXT — a wrapper hands on words that are already parsed, an interpreter
// hands on a string that has to be parsed again. That is a different operation
// with a different precondition (the text must be literal) and a different
// cost (a fresh parse, hence the depth bound), so it is a separate table rather
// than a flag on wrapper.
//
// The set is the POSIX-family shells that share `-c` semantics. `ksh` and
// `fish` from the task's list are here for the same reason: both take `-c` with
// a command string, and both are ordinary things for an agent's environment to
// have. Their scripts are not all bash-compatible — fish's especially — but a
// payload is only re-parsed to find the PROGRAM WORDS in it, and `npm publish`
// is the same two words under every one of these. A fish-specific construct
// this parser misreads costs the invocations inside that construct, which is
// the same as today's answer of not looking at all.
//
// `python -c` and `node -e` are deliberately absent. Their payload is a
// program in another language, and the words in it are not shell words —
// re-parsing `python -c "import os; os.system('npm publish')"` as shell finds
// `import` and `os.system('npm` as programs, which is the fabricated-program
// failure the module calls worse than missing one.
// clustered is whether this interpreter's command-string flag can come as one
// letter inside a short-flag cluster.
//
// True for the shells, where `-lc` and `-euxc` are how agents write it. False
// for `su`, which is not a shell: its `-c` is a long-style option that happens
// to be one letter, and su has no cluster grammar to put it in. Reading `-c`
// out of a cluster for su would be inventing a spelling su does not accept.
type interpreter struct {
	clustered bool
	// positionals is how many bare words the interpreter takes before its
	// options are done with — flock's lock file. Without it the scan stops at
	// that word, reading it as a script file, and the `-c` after it is never
	// reached. Zero for everything whose `-c` comes first, which is every shell.
	positionals int
	// takesValue are this interpreter's OWN flags that consume the following
	// word, beyond the shell-common set in interpreterFlagsTakingValue.
	//
	// Needed because the non-shell interpreters have value-taking options whose
	// VALUE is a bare word, and a bare word is what the scan reads as "options
	// are over, there is no payload". Measured: `su -s /bin/sh -c "npm publish"`
	// reported su alone, because `/bin/sh` was read as the user name and the
	// `-c` after it was never reached. The same for `su -g`, `flock -w`,
	// `flock -E` and runuser's `-s`/`-g`.
	//
	// A silent MISS rather than a fabrication, which is the safer direction —
	// but a miss on a spelling that runs, so a rule about the payload never
	// fired on it. The wrapper table already declared these same flags as
	// value-taking for its own scan; this is the interpreter scan being told
	// what the wrapper scan already knew.
	//
	// Nil for the shells, whose own value-taking options are the shared set.
	takesValue map[string]bool
}

var interpreters = map[string]interpreter{
	"sh":   {clustered: true},
	"bash": {clustered: true},
	"zsh":  {clustered: true},
	"dash": {clustered: true},
	"ksh":  {clustered: true},
	"fish": {clustered: true},

	// `su [options] [-] [user]` with `-c command`. It is BOTH a wrapper-table
	// entry and an interpreter, which the task flagged as belonging to whichever
	// of the two unwrapping tasks landed second — this one.
	//
	// The two roles do not overlap: the wrapper entry declares `-c` as
	// value-taking so the scan does not report the payload as a program word,
	// and this entry re-parses that same payload as the shell script it is. su
	// runs it through the target user's shell, so it is a command string in
	// exactly the sense this path means.
	//
	// `-s`/`-g`/`-G` take a value that is a BARE WORD, which the scan would
	// otherwise read as the user name and stop on — losing the `-c` behind it.
	"su": {takesValue: map[string]bool{
		"-s": true, "--shell": true,
		"-g": true, "--group": true,
		"-G": true, "--supp-group": true,
	}},

	// `flock file -c "npm publish"` runs the payload through `sh -c`, so it is
	// a command string in exactly this sense. Its other spelling,
	// `flock file npm publish`, is a vector and is handled by the wrapper table
	// — the same program appears in both tables for the two different shapes it
	// accepts, which is what su does too. The lock file is a bare word BEFORE
	// the `-c`, so it is spent as a positional rather than mistaken for a
	// script.
	//
	// `-w`/`-E` take a value that is a bare word. Without declaring them the
	// value is spent as the lock-file positional, the real lock file then ends
	// the scan, and the `-c` behind it is never reached.
	"flock": {positionals: 1, takesValue: map[string]bool{
		"-w": true, "--wait": true, "--timeout": true,
		"-E": true, "--conflict-exit-code": true,
	}},

	// `runuser -c "npm publish" user` is the same command-string form as su's,
	// and runuser is likewise in both tables. Same value-taking options as su.
	"runuser": {takesValue: map[string]bool{
		"-s": true, "--shell": true,
		"-g": true, "--group": true,
		"-G": true, "--supp-group": true,
		"-u": true, "--user": true,
	}},
}

// interpreterFlagsTakingValue are the interpreter's own flags that consume the
// word after them.
//
// Only these can stand between the interpreter and its payload while looking
// like a flag. `-o` is the one that matters and it was verified against real
// bash rather than assumed: `bash -o -c 'echo x'` fails with "-c: invalid
// option name", which is bash reading `-c` as -o's VALUE. Without this entry
// that line would be read as `-c` naming the payload `echo x`, reporting a
// program from a command line that runs nothing at all.
var interpreterFlagsTakingValue = map[string]bool{
	"-o": true, "+o": true, "--rcfile": true, "--init-file": true,
}

// interpreterPayload returns the command-string argument of an interpreter
// call, and whether there is one to re-parse.
//
// False for everything that is not an interpreter running a literal command
// string, which includes several cases that look close:
//
//	sh script.sh          no -c. The payload is a FILE, whose contents are not
//	                      in the command line at all.
//	sh -s "npm publish"   -s reads the script from stdin; the string becomes
//	                      $0, not code. Verified against real sh, which runs
//	                      nothing when given it.
//	sh -c "$CMD"          the payload word is not literal — its value is a
//	                      runtime parameter, which is the floor.
//	sh -c                 -c with nothing after it. There is no payload word.
func interpreterPayload(argv []word) (string, bool) {
	in, ok := interpreters[basename(argv[0].value)]
	if !ok {
		return "", false
	}

	// Bare words the interpreter takes before its own options are done —
	// flock's lock file. Counted down as they are passed.
	positionals := in.positionals

	for i := 1; i < len(argv); i++ {
		arg := argv[i].value

		if arg == "--" {
			// Reached only BEFORE any `-c`. A terminator that FOLLOWS a `-c` is
			// stepped over where the payload is chosen below, so it never
			// arrives here — and the difference between the two positions is the
			// whole of what this branch decides.
			//
			// Here options really are over, and a payload can only be named by a
			// flag, so there is none: what follows is a script FILE and its
			// arguments. Real sh agrees — `sh -- -c "npm publish"` answers
			// `-c: No such file or directory`, running nothing.
			return "", false
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			// A bare word before any `-c`. For a shell it is the script FILE,
			// whose contents are not here; for `su` it is the USER. Either way
			// there is nothing to re-parse and nothing to guess.
			//
			// `su - -c "npm publish"` is why the lone `-` is not simply skipped
			// as an option: it is su's login marker, and stopping on it costs
			// only that spelling, where treating it as a flag would risk
			// reading a user name as a payload.
			if positionals > 0 {
				positionals--
				continue
			}
			return "", false
		}
		if interpreterFlagsTakingValue[arg] || in.takesValue[arg] {
			// A flag whose VALUE is the next word. The per-interpreter half
			// matters most when that value is a BARE word — `su -s /bin/sh -c
			// ...` — because a bare word is otherwise read as the user name and
			// ends the scan, losing the `-c` behind it.
			i++
			continue
		}
		if !in.isCommandStringFlag(arg) {
			continue
		}
		// The command-string flag. The payload is the word after it — or the
		// word after a single `--`, which is the option terminator rather than
		// the payload.
		//
		// Verified against real shells rather than reasoned about:
		//
		//	sh -c -- "echo HI"        prints HI      the -- is the terminator
		//	sh -c -- -- 'echo HI'     --: not found  the SECOND -- is the payload
		//
		// So exactly one terminator is stepped over. Reading it as the payload
		// instead lost the real one AND let the `--` fall through to the wrapper
		// path, where it was promoted to a program — a binary nothing invokes,
		// reported off a line that genuinely runs npm. Both failure directions
		// at once, which is why this is spelled out rather than folded into the
		// loop.
		payload := i + 1
		if payload < len(argv) && argv[payload].value == "--" {
			payload++
		}
		if payload >= len(argv) {
			return "", false
		}
		// Literalness is the whole precondition: `sh -c "np${X}m publish"`
		// resolves to the same string as the certain case and must not be read
		// as if it were one.
		if p := argv[payload]; p.literal {
			return p.value, true
		}
		return "", false
	}
	return "", false
}

// isCommandStringFlag reports whether a flag word is the one naming a command
// string.
//
// For a shell this is a CLUSTER test, not equality with `-c`, because the
// clustered spelling is how agents actually write it: `bash -lc`, `bash -ec`,
// `sh -euxc`. A short flag cluster sets every letter in it, so any cluster
// containing `c` names a command string.
//
// Containment rather than "ends with c", which is what a first reading of the
// forms suggests. Verified against real sh: `sh -cx "echo hello"` and
// `sh -xc "echo hello"` both run the payload, so the position of the `c` in
// the cluster carries no meaning. Requiring it last would silently miss
// `-cx` — one letter's difference between a rule firing and not.
//
// Long options are excluded. No `--`-prefixed shell option names a command
// string, and every long option that CONTAINS a c (`--noprofile`, `--rcfile`)
// would be a false positive under a containment test, taking the next word as
// code. `--command=X` is not read either: the payload would be inline rather
// than the next word, and no spelling of it is currently unwrapped.
//
// A non-clustering interpreter takes only the exact `-c`, because it has no
// cluster grammar to read a letter out of.
func (in interpreter) isCommandStringFlag(arg string) bool {
	if strings.HasPrefix(arg, "--") {
		return false
	}
	if !in.clustered {
		return arg == "-c"
	}
	return strings.Contains(arg[1:], "c")
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
//
// depth is how many interpreter payloads have been entered to get here. It is
// carried rather than stored so that two payloads side by side each get the
// full budget — `sh -c 'a' && sh -c 'b'` is not deeper than one of them.
func fromArgv(argv []word, depth int) []Invocation {
	if len(argv) == 0 || basename(argv[0].value) == "" {
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
		// while missing npm. The depth is passed through unchanged — a wrapper
		// is not a new parse, so it spends none of the payload budget.
		invs = append(invs, fromArgv(nested, depth)...)
	}

	// The infix form, which the suffix scan above cannot see: a command
	// delimited by `-exec` and `;` with find's own operands on both sides.
	//
	// Recursive through fromArgv for the same reason, so the shapes compose in
	// both directions: `sudo find . -exec npm publish \;` reaches here having
	// already been unwrapped from the sudo, and `find . -exec sh -c 'npm
	// publish' \;` hands its clause back through the interpreter path. The depth
	// is passed unchanged — like a wrapper and unlike a payload, an infix clause
	// is a slice of a vector already parsed, so it spends none of the budget.
	for _, cmd := range unwrapInfix(argv) {
		invs = append(invs, fromArgv(cmd, depth)...)
	}

	// The interpreter itself has already been appended above and stays
	// reported. Unwrapping a payload ADDS what it runs; a rule about `sh` must
	// not be defeated by the fix to a rule about npm.
	invs = append(invs, fromPayload(argv, depth)...)

	return invs
}

// fromPayload re-parses a literal interpreter payload and returns the
// invocations inside it.
//
// Nothing when this is not an interpreter, when it names no payload, when the
// payload word was not literal, or when the depth budget is spent. Each of
// those is a case where the honest answer is the interpreter alone.
func fromPayload(argv []word, depth int) []Invocation {
	if depth >= maxUnwrapDepth {
		return nil
	}
	payload, ok := interpreterPayload(argv)
	if !ok {
		return nil
	}
	// walkAt, not walk: the payload's own invocations may include another
	// interpreter, and the budget spent getting here has to travel with them.
	return walkAt(payload, depth+1)
}

// unwrap returns the vector a wrapper is wrapping, or nil if this is not a
// wrapper or names nothing to run.
func unwrap(argv []word) []word {
	w, isWrapper := wrappers[basename(argv[0].value)]
	if !isWrapper {
		return nil
	}
	if w.wrapsNothing {
		// A wrapper whose bare words are never a program — `su someuser` names
		// a user. What it runs, if anything, comes from `-c` and is read on the
		// interpreter path.
		return nil
	}

	// Whether the flag that makes bare words mean "program" has been seen. True
	// from the start for every wrapper that has no such condition, which is all
	// of them but runuser.
	enabled := len(w.requiresFlag) == 0

	// How many of the wrapper's own bare words are still to come before the
	// program. Counted down as they are passed, so `timeout 5 npm publish`
	// spends the one on `5` and reports npm.
	positionals := w.positionals

	for i := 1; i < len(argv); i++ {
		arg := argv[i].value

		// `--` ends the wrapper's own options; whatever follows is the command.
		// Its positionals still come first — `timeout -- 5 npm` is the duration
		// then the program, since `--` ends options, not arguments.
		if arg == "--" {
			if !enabled {
				return nil
			}
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
			if !enabled {
				// `runuser someuser` — the flag that would make this a program
				// was never given, so this word is a user name.
				return nil
			}
			return rest(argv, i)
		}
		if w.requiresFlag[arg] {
			enabled = true
		}
		// A `c` in a short flag, on a wrapper that ALSO takes a `-c` command
		// string, ends the vector scan.
		//
		// flock and runuser are in both tables: they accept either a vector or
		// a `-c` string, and takesValue enumerates only the exact `-c`. Any
		// other spelling carrying a c — `flock file -nc "npm publish"` — falls
		// through to the bare-word branch, and the payload is returned AS the
		// vector: one invocation whose bin is the whole string `npm publish`,
		// a binary nothing invokes and a basename no rule can match.
		//
		// Stopping is right whichever way the real program reads the spelling.
		// If it accepts the cluster, the next word is a command STRING and
		// belongs to the interpreter path, not here. If it rejects it — which
		// is what flock does — the line runs nothing at all, and reporting the
		// wrapper alone is the honest answer. Both roads lead away from
		// inventing a program.
		if _, alsoInterpreter := interpreters[basename(argv[0].value)]; alsoInterpreter &&
			!strings.HasPrefix(arg, "--") && strings.Contains(arg[1:], "c") {
			return nil
		}
		if w.suppressedBy[arg] {
			// The wrapper is describing its argument, not running it —
			// `command -v npm`. There is no wrapped invocation to report, and
			// the scan stops rather than continuing to look for one.
			return nil
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
//
// This is the one caller that can ask for an index PAST the end rather than
// merely at it: `timeout --` is two words, the command begins at index 2, and
// the owed positional makes that 3. rest's bounds check is what absorbs it.
func afterPositionals(argv []word, from, positionals int) []word {
	return rest(argv, from+positionals)
}

// rest returns the vector from an index onward, or nil when the index is not
// inside it.
//
// The bounds check is load-bearing and its failure would be SILENT, which is
// why it is worth a sentence. afterPositionals can overshoot the end (see
// there), and `argv[from:]` panics for from > len — a panic `walk` recovers,
// keeping only what it had collected before it. So an out-of-range slice here
// does not crash the guardrail, it truncates the invocation list at the
// offending statement: `timeout -- ; rm -rf /` would report nothing at all, and
// a rule about rm would not fire. Pinned by
// TestNesting_APositionalCountPastTheEndOfTheVectorDoesNotAbandonTheLine, which
// asserts the SIBLING statements survive rather than just the wrapper.
//
// `>=` rather than `>` is deliberate but not observable: at from == len the
// slice is empty and every caller tests len(...) > 0, so the two spellings agree
// and a mutation between them survives the suite. Only from > len separates
// them, and that is the panic the check exists for. Recorded so the survivor is
// read as an equivalence rather than as this guard being untested.
func rest(argv []word, from int) []word {
	if from >= len(argv) {
		return nil
	}
	return argv[from:]
}

// newInvocation builds one invocation from a resolved vector.
//
// The provenance is dropped here and goes no further: Argv is what a rule
// reads, and whether a word was literal is this module's own reasoning about
// certainty, not a fact about the command that a rule should be matching on.
func newInvocation(argv []word) Invocation {
	vals := values(argv)
	return Invocation{
		Bin:   basename(vals[0]),
		Argv:  vals,
		Flags: parseFlags(vals[1:]),
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
