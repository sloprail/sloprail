package commandmod

import (
	"strconv"
	"strings"
	"testing"
)

// This file is the nesting corpus: one case per way a command line can put a
// program somewhere other than the front of the line.
//
// A rule matches on `invocations`. Every form here is a form an agent could
// write, and for each one the question is the same — does the program inside
// reach the list a rule reads? Where it does not, the case says so in `want`
// and the comment says whether that is a decision or a gap, because a suite
// that only asserted the good cases would leave the gaps unwritten and
// therefore unreviewable.

// binsOf is the flattened program list, which is what a nesting case asserts on.
func binsOf(src string) []string {
	got := []string{}
	for _, inv := range ExtractCommand(src).Invocations {
		got = append(got, inv.Bin)
	}
	return got
}

func assertBins(t *testing.T, src string, want []string) {
	t.Helper()
	if got := binsOf(src); !equal(got, want) {
		t.Errorf("ExtractCommand(%q) bins = %v, want %v", src, got, want)
	}
}

// squote wraps a payload in single quotes, escaping any it already contains, so
// nesting an interpreter payload inside another stays ONE word. Building the
// nesting cases by hand is where a depth test quietly stops testing depth —
// a mis-quoted level parses as several words and the payload is never one
// string at all.
func squote(s string) string {
	return "'" + strings.ReplaceAll(s, `'`, `'\''`) + "'"
}

func itoa(n int) string { return strconv.Itoa(n) }

// countOf is how many times a program appears, for the cases where a program
// running twice is the assertion.
func countOf(hay []string, needle string) int {
	n := 0
	for _, h := range hay {
		if h == needle {
			n++
		}
	}
	return n
}

// TestNesting_ShellOperators: every operator that joins statements. Each one
// nests a program one level deeper than a string match would look, and none of
// them may hide it.
func TestNesting_ShellOperators(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		{"pipe", `npm publish | tee log`, []string{"npm", "tee"}},
		{"pipe both streams", `npm publish |& tee log`, []string{"npm", "tee"}},
		{"and", `cd /x && npm publish`, []string{"cd", "npm"}},
		{"or", `npm ping || npm publish`, []string{"npm", "npm"}},
		{"semicolon", `cd /x; npm publish`, []string{"cd", "npm"}},
		{"background", `npm publish &`, []string{"npm"}},
		{"background then more", `npm publish & npm whoami`, []string{"npm", "npm"}},
		{"newline separated", "cd /x\nnpm publish", []string{"cd", "npm"}},
		{"subshell", `(npm publish)`, []string{"npm"}},
		{"nested subshell", `( ( ( npm publish ) ) )`, []string{"npm"}},
		{"group", `{ npm publish; }`, []string{"npm"}},
		{"group inside subshell", `( { npm publish; } )`, []string{"npm"}},
		{"negation", `! npm publish`, []string{"npm"}},
		{"time clause", `time npm publish`, []string{"npm"}},
		{"time on a group", `time { npm publish; }`, []string{"npm"}},
		{"coproc", `coproc npm publish`, []string{"npm"}},
		// A chain mixing everything. The point is that depth composes: no
		// combination of wrappers and operators loses a program that any one of
		// them alone would keep. `x` is the payload's own program, reported now
		// that a literal `-c` string is re-parsed.
		{"mixed chain", `sudo sh -c 'x' && (time npm publish | tee log)`, []string{"sudo", "sh", "x", "npm", "tee"}},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBins(t, tc.src, tc.want) })
	}
}

// TestNesting_ControlFlow: the bodies of compound statements. These are not
// CallExpr at the top — a traversal matching only calls would see through none
// of them — and a program in a loop body is as much about to run as one on the
// first line.
func TestNesting_ControlFlow(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		// Every branch is reported, including ones that will not be taken. A
		// rule asks what the line can run, not which branch wins at runtime —
		// deciding that would mean running the condition.
		{"if", `if npm ping; then npm publish; fi`, []string{"npm", "npm"}},
		{"if else", `if npm ping; then npm publish; else npm whoami; fi`, []string{"npm", "npm", "npm"}},
		{"elif", `if a; then b; elif c; then npm publish; fi`, []string{"a", "b", "c", "npm"}},
		{"while", `while npm ping; do npm publish; done`, []string{"npm", "npm"}},
		{"until", `until npm ping; do npm publish; done`, []string{"npm", "npm"}},
		{"for", `for f in a b; do npm publish; done`, []string{"npm"}},
		{"c-style for", `for ((i=0;i<3;i++)); do npm publish; done`, []string{"npm"}},
		{"select", `select f in a b; do npm publish; done`, []string{"npm"}},
		{"case", `case $x in a) npm publish;; esac`, []string{"npm"}},
		{"case multiple arms", `case $x in a) npm publish;; b) npm whoami;; esac`, []string{"npm", "npm"}},

		// A function body is walked where it is defined, and the call is its own
		// invocation. Both are right and neither substitutes for the other: the
		// body is what will run, and `deploy` is a program name a rule could
		// legitimately be written against, since nothing here can prove the name
		// resolves to the function rather than to a binary on PATH.
		{"function definition and call", `deploy() { npm publish; }; deploy`, []string{"npm", "deploy"}},
		{"function keyword form", `function deploy { npm publish; }; deploy`, []string{"npm", "deploy"}},

		// The body is reported even when nothing calls it. That is the honest
		// answer: whether it is called can depend on arguments, and a body that
		// is present is a program the line can reach.
		{"function never called", `deploy() { npm publish; }`, []string{"npm"}},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBins(t, tc.src, tc.want) })
	}
}

// TestNesting_Substitutions: every way one command's text is produced by
// another. The inner statements are walked as syntax and never run — proven
// separately in TestExtractCommand_ResolvesNothingUnsafe — so the inner program
// is reported without the substitution ever executing.
func TestNesting_Substitutions(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		{"command substitution as argument", `echo $(npm publish)`, []string{"echo", "npm"}},
		{"command substitution as whole line", `$(npm publish)`, []string{"npm"}},
		{"backticks", "echo `npm publish`", []string{"echo", "npm"}},
		{"backticks as whole line", "`npm publish`", []string{"npm"}},
		{"nested substitution", `echo $(echo $(npm publish))`, []string{"echo", "echo", "npm"}},
		{"substitution in a redirect target", `echo hi > $(npm config get x)`, []string{"echo", "npm"}},

		// Process substitution. The receiving command keeps its vector shape —
		// the argument becomes the path a shell would have handed it — and the
		// program inside is reported. Without the ProcSubst handler this panics
		// rather than returning, which is why it has its own test too.
		{"process substitution in", `diff <(npm publish)`, []string{"diff", "npm"}},
		{"process substitution out", `tee >(npm publish)`, []string{"tee", "npm"}},
		{"two process substitutions", `diff <(npm ping) <(npm publish)`, []string{"diff", "npm", "npm"}},

		// The program word itself is a substitution, so it cannot be known
		// without running it. `which` inside is genuinely visible; nothing is
		// invented for the outer program.
		{"substituted program word", `$(which npm) publish`, []string{"which"}},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBins(t, tc.src, tc.want) })
	}

	// The process substitution's argument is the substituted path, not the text
	// of the inner command — the vector keeps the shape the program will see.
	invs := ExtractCommand(`diff <(npm publish)`).Invocations
	if len(invs) == 0 || len(invs[0].Argv) != 2 || invs[0].Argv[1] != procSubstPath {
		t.Errorf("diff argv = %v, want [diff %s]", invs[0].Argv, procSubstPath)
	}
}

// TestNesting_Wrappers: a program whose own arguments name another program.
//
// Both are emitted. A rule about npm is defeated by `sudo npm publish` if only
// sudo is reported, and a rule about sudo is defeated by reporting only npm.
func TestNesting_Wrappers(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		{"sudo", `sudo npm publish`, []string{"sudo", "npm"}},
		{"sudo separated flag value", `sudo -u root npm publish`, []string{"sudo", "npm"}},
		{"sudo inline flag value", `sudo --user=root npm publish`, []string{"sudo", "npm"}},
		{"sudo valueless flag", `sudo -E npm publish`, []string{"sudo", "npm"}},
		{"sudo double dash", `sudo -- npm publish`, []string{"sudo", "npm"}},
		{"sudo flag then double dash", `sudo -u root -- npm publish`, []string{"sudo", "npm"}},

		// `--` must END the wrapper's options, not merely be skipped as one more
		// flag. The two readings only diverge when the wrapped program is itself
		// flag-shaped: skipping `--` would keep scanning, treat `--npm` as
		// another of sudo's own flags, and walk off the end reporting nothing
		// wrapped. A program whose name starts with a dash is exactly what an
		// agent would reach for to exploit that.
		{"double dash then a dash-shaped program", `sudo -- --npm publish`, []string{"sudo", "--npm"}},
		{"double dash then a short-dash program", `sudo -- -npm publish`, []string{"sudo", "-npm"}},
		{"flag value then double dash then dash-shaped", `sudo -u root -- -npm x`, []string{"sudo", "-npm"}},
		{"xargs double dash then dash-shaped", `xargs -- --version`, []string{"xargs", "--version"}},
		{"doas", `doas npm publish`, []string{"doas", "npm"}},
		{"env", `env npm publish`, []string{"env", "npm"}},
		{"env with assignment", `env FOO=1 npm publish`, []string{"env", "npm"}},
		{"env with several assignments", `env A=1 B=2 npm publish`, []string{"env", "npm"}},
		{"env -i", `env -i npm publish`, []string{"env", "npm"}},
		{"env unset then program", `env -u PATH npm publish`, []string{"env", "npm"}},
		{"env double dash", `env -- npm publish`, []string{"env", "npm"}},
		{"nohup", `nohup npm publish`, []string{"nohup", "npm"}},
		{"nice", `nice npm publish`, []string{"nice", "npm"}},
		{"nice with adjustment", `nice -n 5 npm publish`, []string{"nice", "npm"}},
		{"ionice", `ionice -c 3 npm publish`, []string{"ionice", "npm"}},
		{"stdbuf", `stdbuf -o0 npm publish`, []string{"stdbuf", "npm"}},
		{"xargs", `xargs npm publish`, []string{"xargs", "npm"}},
		{"xargs -n", `xargs -n 1 npm publish`, []string{"xargs", "npm"}},
		{"xargs -n inline", `xargs -n1 npm publish`, []string{"xargs", "npm"}},
		{"xargs -I separated", `xargs -I {} npm publish {}`, []string{"xargs", "npm"}},
		{"xargs -0", `xargs -0 npm publish`, []string{"xargs", "npm"}},
		{"xargs double dash", `xargs -- npm publish`, []string{"xargs", "npm"}},

		// Wrappers stack, and unwrapping recurses. Stopping at the first would
		// report sudo and lose everything behind it.
		{"two wrappers", `sudo nohup npm publish`, []string{"sudo", "nohup", "npm"}},
		{"three wrappers", `sudo env FOO=1 nice npm publish`, []string{"sudo", "env", "nice", "npm"}},

		// The wrapped program keeps its path in argv and is reduced to a
		// basename in bin, exactly as an unwrapped one is — the wrapper is not a
		// place where the path-spelling evasion starts working again.
		{"wrapped path program", `sudo /usr/local/bin/npm publish`, []string{"sudo", "npm"}},

		// A lone `-` is not a flag. It is conventionally stdin, and the
		// unwrapper must stop there and treat it as the wrapped program word
		// rather than skipping past it as an option — skipping would make the
		// NEXT word the reported program, which is a name the wrapper never
		// runs.
		{"lone dash as the wrapped word", `sudo - npm publish`, []string{"sudo", "-"}},
		{"lone dash after a flag", `env - npm publish`, []string{"env", "-"}},

		// A word STARTING with `=` is not an assignment prefix — there is no
		// variable name in front of the equals, so env does not consume it and
		// neither does this. It is the program word, odd as it looks. Skipping
		// it as an assignment would report `npm` as what env wraps, when env
		// would really have tried to run `=weird`.
		{"equals-leading word is the program", `env =weird npm publish`, []string{"env", "=weird"}},
		{"equals-leading word under sudo", `sudo =x npm publish`, []string{"sudo", "=x"}},

		// A wrapper naming nothing to run. It is genuinely invoked and is
		// reported; there is no second invocation to invent.
		{"bare sudo", `sudo`, []string{"sudo"}},
		{"bare xargs", `xargs`, []string{"xargs"}},
		{"sudo with only flags", `sudo -u root`, []string{"sudo"}},
		{"env with only assignments", `env FOO=1`, []string{"env"}},

		// The wrapped program word is an empty string. sudo ran; the thing it
		// wrapped has no name, and an empty bin reaching a matcher would be a
		// program no rule can mean.
		{"wrapper wrapping an empty word", `sudo "" publish`, []string{"sudo"}},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBins(t, tc.src, tc.want) })
	}
}

// TestNesting_WrapperArgvIsTheWrappedVector: unwrapping hands on the vector
// starting at the program, not the wrapper's whole line. If it handed on the
// wrapper's own arguments too, `npm`'s argv would carry `-u root` and a rule
// reading argv would see flags npm never receives.
func TestNesting_WrapperArgvIsTheWrappedVector(t *testing.T) {
	invs := ExtractCommand(`sudo -u root npm publish --tag=next`).Invocations
	if len(invs) != 2 {
		t.Fatalf("got %d invocations, want 2", len(invs))
	}
	if !equal(invs[0].Argv, []string{"sudo", "-u", "root", "npm", "publish", "--tag=next"}) {
		t.Errorf("sudo argv = %v, want the whole line", invs[0].Argv)
	}
	if !equal(invs[1].Argv, []string{"npm", "publish", "--tag=next"}) {
		t.Errorf("npm argv = %v, want the wrapped vector only", invs[1].Argv)
	}
	// The wrapper's own flags belong to the wrapper. npm must not be reported as
	// carrying `-u`, or a rule about `npm -u` fires on a command where root was
	// sudo's argument.
	if _, ok := invs[1].Flags["u"]; ok {
		t.Errorf("npm flags = %v, must not carry sudo's own flags", invs[1].Flags)
	}
	if invs[1].Flags["tag"] != "next" {
		t.Errorf("npm flags = %v, want tag=next", invs[1].Flags)
	}
}

// TestNesting_InterpreterPayloadsAreOpaque is now the FLOOR half of what this
// test used to cover, and the floor only.
//
// It used to pin every interpreter payload as unread, with each case carrying a
// `decided` flag saying whether that was a decision or a gap. The gap cases are
// closed — see TestNesting_LiteralInterpreterPayloadsAreUnwrapped — and what
// remains here is the set where not looking is the right answer, because the
// payload does not exist as text at the moment of the check.
//
// The distinction every case here turns on is LITERALNESS, which is the test
// resolve.go already applies to the program word. A payload that is exactly
// what was typed can be re-parsed; one whose value depends on the runtime
// cannot, and reporting a program read out of it would be a guess about the
// environment dressed up as a reading of the command.
//
// These must stay red-on-change in the OTHER direction: if one of them starts
// reporting its hidden program, the module has begun guessing, and that is a
// regression rather than an improvement.
func TestNesting_InterpreterPayloadsAreOpaque(t *testing.T) {
	for _, tc := range []struct {
		name     string
		src      string
		want     []string
		unseen   string // the program that really runs and must not be guessed at
		whyRight string
	}{
		{
			name: "eval", src: `eval "npm publish"`, want: []string{"eval"}, unseen: "npm",
			whyRight: "eval's argument is re-interpreted at runtime after expansion; re-parsing has no bottom",
		},
		{
			name: "sh -c from a variable", src: `sh -c "$CMD"`, want: []string{"sh"}, unseen: "npm",
			whyRight: "the payload is a parameter; its value is not knowable without the runtime environment",
		},
		{
			// The case that makes literalness the right test rather than
			// provenance or shape. This payload RESOLVES to `npm publish` under
			// the empty environment and is character-for-character identical to
			// the literal case once expanded — only isLiteral separates them.
			name: "sh -c with an interpolation that resolves to a real command",
			src:  `sh -c "np${X}m publish"`, want: []string{"sh"}, unseen: "npm",
			whyRight: "it only looks like npm because the empty environment assumed ${X} was empty; at runtime it is anything",
		},
		{
			name: "sh -c with an interpolated argument", src: `sh -c "npm publish --tag $TAG"`,
			want: []string{"sh"}, unseen: "npm",
			whyRight: "any non-literal part makes the whole payload uncertain; isLiteral is a property of the word, not of its prefix",
		},
		{
			name: "sh -c from a command substitution", src: `sh -c "$(cat run.sh)"`,
			want: []string{"sh", "cat"}, unseen: "npm",
			whyRight: "the payload does not exist until cat runs; cat itself is reported because it genuinely is about to run",
		},
		{
			name: "base64 decoded and piped to sh", src: `echo cm0gLXJmIC8= | base64 -d | sh`,
			want: []string{"echo", "base64", "sh"}, unseen: "rm",
			whyRight: "the decoded payload does not exist until base64 runs",
		},
		{
			// A script FILE, not a command string. The contents are on disk,
			// and reading disk to decide what a command means is what the
			// module refuses.
			name: "sh running a script file", src: `sh deploy.sh`, want: []string{"sh"}, unseen: "npm",
			whyRight: "the payload is a file; its contents are not in the command line",
		},
		{
			// `-s` reads the script from STDIN and the string becomes $0.
			// Verified against real sh: `sh -s "echo x"` runs nothing.
			name: "sh -s does not run its argument", src: `sh -s "npm publish"`, want: []string{"sh"}, unseen: "npm",
			whyRight: "-s reads the script from stdin; the string is $0, not code",
		},
		{
			// `python -c` is a payload in another language. Re-parsing it as
			// shell would report `import` and `os.system('npm` as programs —
			// fabricated names, which the module calls worse than missing one.
			name: "python -c is not a shell payload", src: `python -c "import os; os.system('npm publish')"`,
			want: []string{"python"}, unseen: "npm",
			whyRight: "the payload is Python, and its words are not shell words",
		},

		// A LONG option is never the command-string flag, and several contain
		// a `c`. Under a containment test that did not exclude them, `--norc`
		// and `--rcfile` read as naming a payload and the next word is taken as
		// code — so `sh --norc npm publish` reports a payload parsed out of
		// `npm`, which is a program word invented from an option name.
		//
		// Measured: removing the long-option exclusion changes exactly these
		// lines and nothing else in the suite noticed, so they are pinned here.
		{
			name: "long option containing c is not the payload flag", src: `sh --norc npm publish`,
			want: []string{"sh"}, unseen: "npm",
			whyRight: "--norc is an option, not -c; the bare word after it is a script FILE",
		},
		{
			name: "rcfile takes a value and is not the payload flag", src: `bash --rcfile npm publish`,
			want: []string{"bash"}, unseen: "npm",
			whyRight: "--rcfile names a startup file; nothing here is a command string",
		},
		{
			name: "login is not the payload flag", src: `bash --login npm publish`,
			want: []string{"bash"}, unseen: "npm",
			whyRight: "--login contains no c at all, and is still not a payload flag",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertBins(t, tc.src, tc.want)
			if contains(binsOf(tc.src), tc.unseen) {
				t.Errorf("ExtractCommand(%q) now reports %q — this is the resolution FLOOR, not a gap: "+
					"%s. Reporting it means the module started guessing.", tc.src, tc.unseen, tc.whyRight)
			}
		})
	}
}

// TestNesting_LiteralInterpreterPayloadsAreUnwrapped is the gap this work
// closed, pinned as behaviour.
//
// `sh -c "npm publish"` runs npm, and the payload is right there in the text.
// While it went unread, a rule about npm SILENTLY NEVER FIRED on it — which is
// the failure the product exists to prevent, because a rule that never fires
// looks exactly like a rule being satisfied. A harness that wraps everything in
// `bash -lc` made every command rule cover nothing.
//
// The interpreter is still reported in every case. Unwrapping ADDS what the
// payload runs; a rule about `sh` must not be defeated by the fix to a rule
// about npm.
func TestNesting_LiteralInterpreterPayloadsAreUnwrapped(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		{"sh -c", `sh -c "npm publish"`, []string{"sh", "npm"}},
		{"sh -c single quoted", `sh -c 'npm publish'`, []string{"sh", "npm"}},
		{"bash -c", `bash -c "npm publish"`, []string{"bash", "npm"}},
		{"zsh -c", `zsh -c 'npm publish'`, []string{"zsh", "npm"}},
		{"dash -c", `dash -c 'npm publish'`, []string{"dash", "npm"}},
		{"ksh -c", `ksh -c 'npm publish'`, []string{"ksh", "npm"}},
		{"fish -c", `fish -c 'npm publish'`, []string{"fish", "npm"}},

		// The payload is a whole command line, not a single program, so
		// everything in it is reported.
		{"bash -lc chain", `bash -lc 'cd /x && npm publish'`, []string{"bash", "cd", "npm"}},
		{"payload pipeline", `sh -c 'npm pack | tee out'`, []string{"sh", "npm", "tee"}},
		{"payload with a wrapper inside", `sh -c 'sudo npm publish'`, []string{"sh", "sudo", "npm"}},
		{"payload with a substitution", `sh -c 'echo $(npm view)'`, []string{"sh", "echo", "npm"}},

		// The clustered spellings, which is how agents usually write it. A
		// short-flag cluster sets every letter in it.
		{"bash -lc", `bash -lc "npm publish"`, []string{"bash", "npm"}},
		{"bash -ec", `bash -ec "npm publish"`, []string{"bash", "npm"}},
		{"bash -euxc", `bash -euxc "npm publish"`, []string{"bash", "npm"}},
		{"sh -ic", `sh -ic "npm publish"`, []string{"sh", "npm"}},

		// `c` need not be LAST in the cluster. Verified against real sh:
		// `sh -cx "echo hello"` and `sh -xc "echo hello"` both run the payload,
		// so requiring it last would miss a form that really runs.
		{"sh -cx", `sh -cx "npm publish"`, []string{"sh", "npm"}},
		{"sh -xc", `sh -xc "npm publish"`, []string{"sh", "npm"}},

		// Separated flags before the payload.
		{"bash -x -c", `bash -x -c "npm publish"`, []string{"bash", "npm"}},
		{"bash -o pipefail -c", `bash -o pipefail -c "npm publish"`, []string{"bash", "npm"}},

		// Behind a wrapper, and in front of one. Unwrapping composes with the
		// wrapper table in both directions.
		{"sudo sh -c", `sudo sh -c 'npm publish'`, []string{"sudo", "sh", "npm"}},
		{"xargs sh -c", `xargs sh -c 'npm publish'`, []string{"xargs", "sh", "npm"}},
		{"env sh -c", `env FOO=1 sh -c 'npm publish'`, []string{"env", "sh", "npm"}},
		{"timeout sh -c", `timeout 5 sh -c 'npm publish'`, []string{"timeout", "sh", "npm"}},
		{"sh -c wrapping sudo", `sh -c 'sudo npm publish'`, []string{"sh", "sudo", "npm"}},

		// A path-spelled interpreter is the same interpreter. The basename
		// reduction that stops `/usr/local/bin/npm` evading a rule about npm
		// has to apply here too, or `/bin/sh -c` is a hole.
		{"path spelled interpreter", `/bin/sh -c 'npm publish'`, []string{"sh", "npm"}},
		{"usr bin env bash", `/usr/bin/env bash -c 'npm publish'`, []string{"env", "bash", "npm"}},

		// The payload sits inside the shell structures the module already
		// flattens, so the two compose.
		{"payload in a pipeline", `sh -c 'npm publish' | tee log`, []string{"sh", "npm", "tee"}},
		{"payload in a subshell", `(sh -c 'npm publish')`, []string{"sh", "npm"}},
		{"two payloads", `sh -c 'npm pack' && sh -c 'npm publish'`, []string{"sh", "npm", "sh", "npm"}},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBins(t, tc.src, tc.want) })
	}
}

// TestNesting_InterpreterIsStillReported is the half of the fix that is easiest
// to lose and hardest to notice.
//
// Unwrapping is an ADDITION. An implementation that replaced the interpreter
// with what it runs would close the npm gap and open a `sh` one — a rule about
// shelling out would stop firing on every line that shells out, which is the
// same silent-never-fires failure pointed at a different rule.
func TestNesting_InterpreterIsStillReported(t *testing.T) {
	for _, src := range []string{
		`sh -c "npm publish"`,
		`bash -lc 'cd /x && npm publish'`,
		`sudo sh -c 'npm publish'`,
		`sh -c 'sh -c "npm publish"'`,
	} {
		t.Run(src, func(t *testing.T) {
			got := binsOf(src)
			// Fatal, not Error. The next assertion indexes got[0], and an
			// implementation that REPLACED the interpreter with its payload
			// leaves this empty — so continuing would panic and bury the clear
			// message under a stack trace. Measured: the mutant that swaps the
			// append for an assignment produced exactly that, and the panic
			// masked this diagnosis until the check became fatal.
			if len(got) == 0 {
				t.Fatalf("bins is empty for %q — the interpreter itself is no longer "+
					"reported; unwrapping must ADD to the list, not replace what wraps", src)
			}
			if !contains(got, "sh") && !contains(got, "bash") {
				t.Fatalf("bins = %v — the interpreter itself is no longer reported; "+
					"unwrapping must add to the list, not replace what wraps", got)
			}
			// The interpreter comes FIRST. It is the program the line names,
			// and the payload's programs are nested inside it.
			if got[0] != "sh" && got[0] != "bash" && got[0] != "sudo" {
				t.Errorf("bins = %v, want the interpreter or its wrapper first", got)
			}
		})
	}
}

// TestNesting_InterpreterPayloadDepthIsBounded pins the recursion bound.
//
// A payload can contain another interpreter, and the nesting has no natural
// end: `sh -c 'sh -c "sh -c ..."'` is a string an agent can write as long as it
// likes, and each level is a fresh parse of the whole remaining text. The
// recover in walk catches a panic but not a hang, and a guardrail that hangs is
// as bad as one that crashes.
//
// The bound is maxUnwrapDepth. What matters at the limit is the DIRECTION of
// the shortfall: every interpreter on the way down is still reported, so a rule
// about `sh` fires at any depth, and only the innermost payload goes unread.
// Returning nothing at the limit would be the worse failure — it would make
// deep nesting a way to hide the outer levels too.
func TestNesting_InterpreterPayloadDepthIsBounded(t *testing.T) {
	// The bound is pinned to a LITERAL, not read from the constant.
	//
	// Every other assertion here derives its cases from maxUnwrapDepth, which
	// makes them move with it: changing the constant to 3 or 5 leaves them all
	// passing, because the boundary they walk slides along too. That was
	// measured — mutants setting the bound to 3 and to 5 both survived the
	// suite until this line existed.
	//
	// So the chosen depth is asserted directly. The number is a decision with
	// an argument behind it (see maxUnwrapDepth), and changing it should have
	// to be deliberate: this is the line that makes someone read that argument
	// before moving it.
	if maxUnwrapDepth != 4 {
		t.Fatalf("maxUnwrapDepth = %d, want 4 — the bound is a decision, not a "+
			"tuning knob. Read the argument on the constant: one level is the "+
			"ordinary `bash -lc`, two is a harness wrapping an agent's own `sh -c`, "+
			"three is slack for a layer nobody planned, four is past every real "+
			"form. If you are changing it, update that argument too.", maxUnwrapDepth)
	}

	// nest builds `sh -c 'sh -c ... npm publish'` n interpreters deep, quoting
	// each level so the payload survives as one word.
	nest := func(n int) string {
		src := `npm publish`
		for i := 0; i < n; i++ {
			src = `sh -c ` + squote(src)
		}
		return src
	}

	// The boundary at its literal depth, independent of the constant. Four
	// levels reach npm and five do not — the same two facts the derived cases
	// below assert, written so they cannot slide.
	if got := binsOf(nest(4)); !contains(got, "npm") {
		t.Errorf("bins = %v, want npm reached at exactly 4 interpreters deep", got)
	}
	if got := binsOf(nest(5)); contains(got, "npm") {
		t.Errorf("bins = %v, want npm NOT reached at 5 interpreters deep", got)
	}

	// Inside the budget every level is read, and npm at the bottom is reported.
	for n := 1; n <= maxUnwrapDepth; n++ {
		t.Run("depth "+itoa(n)+" reaches npm", func(t *testing.T) {
			got := binsOf(nest(n))
			if !contains(got, "npm") {
				t.Errorf("bins = %v, want npm found at depth %d (budget is %d)", got, n, maxUnwrapDepth)
			}
			if want := n + 1; len(got) != want {
				t.Errorf("bins = %v (%d), want %d — every sh plus npm", got, len(got), want)
			}
		})
	}

	// One past the budget: the innermost payload is not read, and that is the
	// bound doing its job rather than a bug.
	t.Run("past the budget the innermost payload is unread", func(t *testing.T) {
		got := binsOf(nest(maxUnwrapDepth + 1))
		if contains(got, "npm") {
			t.Errorf("bins = %v — npm was reached %d levels deep, past the bound of %d; "+
				"the recursion is not bounded where it says it is", got, maxUnwrapDepth+1, maxUnwrapDepth)
		}
		// The load-bearing half. Every interpreter on the way down is still
		// there, so a rule about `sh` still fires — the shortfall is the
		// innermost payload only, not the line.
		if want := maxUnwrapDepth + 1; len(got) != want {
			t.Errorf("bins = %v (%d), want %d interpreters — hitting the bound must not "+
				"discard the levels already walked", got, len(got), want)
		}
		for _, b := range got {
			if b != "sh" {
				t.Errorf("bins = %v, want every entry to be sh", got)
				break
			}
		}
	})

	// Depth is spent per PATH, not per line. Two payloads side by side each get
	// the full budget, or a long line would starve its own later statements.
	t.Run("sibling payloads each get the full budget", func(t *testing.T) {
		deep := nest(maxUnwrapDepth)
		got := binsOf(deep + " && " + deep)
		if n := countOf(got, "npm"); n != 2 {
			t.Errorf("bins = %v, want npm twice — each statement gets its own budget, got %d", got, n)
		}
	})

	// Wrapper nesting is NOT bounded by the payload budget and must not become
	// so: it consumes words from a finite vector, so it terminates on its own.
	// 100 stacked sudos inside a payload still reach npm.
	t.Run("wrapper depth inside a payload is not charged to the budget", func(t *testing.T) {
		got := binsOf(`sh -c '` + strings.Repeat("sudo ", 100) + `npm publish'`)
		if !contains(got, "npm") {
			t.Errorf("bins has %d entries and no npm; wrapper stacking must not spend the payload budget", len(got))
		}
	})

	// A payload that will not parse yields nothing from the payload rather than
	// a guess, and does not take the interpreter with it.
	t.Run("an unparseable payload loses only the payload", func(t *testing.T) {
		assertBins(t, `sh -c 'if ['`, []string{"sh"})
		assertBins(t, `sh -c '((('`, []string{"sh"})
		// And it does not abandon the rest of the line.
		assertBins(t, `sh -c '((('  ; npm publish`, []string{"sh", "npm"})
	})
}

// TestNesting_FindExecIsNotUnwrapped: `find . -exec npm publish \;` runs npm,
// and only `find` is reported. find is not in the wrapper table.
//
// Pinned as current behaviour. Unlike `sh -c`, this one is unambiguous to
// unwrap — the vector between `-exec` and `;` is the command, spelled out — so
// it is a gap with a clear fix rather than a resolution floor. Owned by the
// `unwrap-exec-style-wrappers` task in the strategy backlog.
func TestNesting_FindExecIsNotUnwrapped(t *testing.T) {
	for _, src := range []string{
		`find . -exec npm publish \;`,
		`find . -exec npm publish {} +`,
		`find . -execdir npm publish \;`,
		`find . -ok npm publish \;`,
	} {
		t.Run(src, func(t *testing.T) {
			got := binsOf(src)
			if !equal(got, []string{"find"}) {
				t.Errorf("ExtractCommand(%q) bins = %v; want [find] as currently behaves — "+
					"if this now reports the -exec program the gap is closed, update the case", src, got)
			}
			// The words are at least present in find's own argv, so a rule can
			// reach them with a substring match on argv even though no
			// invocation names them.
			invs := ExtractCommand(src).Invocations
			if !contains(invs[0].Argv, "npm") {
				t.Errorf("find argv = %v, want the -exec words carried through", invs[0].Argv)
			}
		})
	}
}

// TestNesting_UnlistedWrappersAreNotUnwrapped pins which programs the wrapper
// table does not know. Each of these runs the program named after it, and only
// the wrapper is reported.
//
// A pinned list rather than a silent absence: adding one of these to `wrappers`
// should be a change that turns a named case red, so the table's contents stay
// a decision somebody made rather than the set nobody got round to extending.
//
// Owned by the `unwrap-interpreter-payloads` task in the strategy backlog,
// which carries the list and the judgement calls (`command`/`exec`/`builtin`
// are shell builtins, `su -c` is also an interpreter payload).
func TestNesting_UnlistedWrappersAreNotUnwrapped(t *testing.T) {
	for _, src := range []string{
		// `find -exec` is a vector between the flag and a terminator, which is
		// a different shape from either table and is owned by the
		// `unwrap-exec-style-wrappers` task. Its own test covers the spellings;
		// it stays listed here so the checklist remains the one place to look.
		`find . -exec npm publish \;`,

		// A program is not a wrapper merely because a program name follows it.
		// These take one as an ARGUMENT and do not run it, so reporting npm
		// would be a rule firing on a line that never invokes npm.
		`which npm`,
		`type npm`,
		`whereis npm`,
		`echo npm publish`,

		// Interpreters whose payload is a program in ANOTHER language.
		// Re-parsing these as shell fabricates program names out of syntax:
		// `import` and `os.system('npm` are not programs.
		`python -c "import os; os.system('npm publish')"`,
		`node -e "require('child_process').exec('npm publish')"`,
		`perl -e "system('npm publish')"`,
		`ruby -e "system('npm publish')"`,
	} {
		t.Run(src, func(t *testing.T) {
			if got := binsOf(src); contains(got, "npm") {
				t.Errorf("ExtractCommand(%q) bins = %v — %q is now unwrapped. If that is "+
					"deliberate, move this case to the test for the table it joined and say why "+
					"the program it names is really about to run", src, got, strings.Fields(src)[0])
			}
		})
	}
}

// TestNesting_PreviouslyUnlistedWrappersAreNowUnwrapped is the other side of
// the checklist: the entries that moved OUT of it, each with the reason it
// belongs in a table.
//
// Every one of these runs the program named after it, so while it went
// unlisted a rule about that program silently never fired on the line.
func TestNesting_PreviouslyUnlistedWrappersAreNowUnwrapped(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
		why  string
	}{
		{`setsid npm publish`, []string{"setsid", "npm"},
			"runs the program in a new session; what follows is a vector"},
		{`unbuffer npm publish`, []string{"unbuffer", "npm"},
			"runs the program under a pty; what follows is a vector"},
		{`watch npm publish`, []string{"watch", "npm"},
			"runs the program repeatedly; repetition does not make it less run"},
		{`watch -n 5 npm publish`, []string{"watch", "npm"},
			"the interval is watch's own argument, taken behind its flag"},
		{`torify npm publish`, []string{"torify", "npm"},
			"runs the program through a proxy; what follows is a vector"},
		{`proxychains npm publish`, []string{"proxychains", "npm"}, "same as torify"},
		{`proxychains -f c.conf npm publish`, []string{"proxychains", "npm"},
			"the config is proxychains's own argument"},

		// A mandatory bare word before the command, the same shape as
		// timeout's duration. Without the positional count the path would be
		// reported as a program AND the real one lost — both failure directions
		// at once, which is what TestNesting_Timeout... settles for timeout.
		{`chroot / npm publish`, []string{"chroot", "npm"}, "NEWROOT comes before the command"},
		{`flock /tmp/l npm publish`, []string{"flock", "npm"}, "the lock file comes before the command"},

		// Shell BUILTINS, which the task flagged as a separate judgement. Being
		// a builtin is not a reason to omit them: what this module reports is
		// what is about to run, and the word after each of these is a program
		// the line is trying to run. That the shell resolves them without an
		// exec is invisible here and changes nothing a rule needs to know.
		{`exec npm publish`, []string{"exec", "npm"},
			"exec REPLACES the shell with npm, so npm runs more definitely than under sudo, which can still refuse"},
		{`exec -a other npm publish`, []string{"exec", "npm"},
			"-a renames the program; the name is exec's argument, not the program"},
		{`command npm publish`, []string{"command", "npm"},
			"runs npm bypassing functions and aliases — precisely the spelling used to get the REAL npm"},
		{`builtin npm publish`, []string{"builtin", "npm"},
			"npm is not a builtin so this runs nothing; but whether a program EXISTS is not a question a static reader answers, cf. nice 10 reporting 10"},

		// Both a wrapper-table entry and an interpreter payload, which the task
		// flagged as belonging to whichever unwrapping task landed second.
		{`su -c "npm publish"`, []string{"su", "npm"}, "the -c payload is a literal command string"},
		{`su -c "npm publish" someuser`, []string{"su", "npm"},
			"the trailing bare word is the USER; only the -c payload is a command"},
		{`runuser -u x npm publish`, []string{"runuser", "npm"}, "with -u the bare word is the program"},
		{`runuser -c "npm publish" x`, []string{"runuser", "npm"}, "the -c payload is a command string"},
		{`flock /tmp/l -c "npm publish"`, []string{"flock", "npm"},
			"flock's -c payload runs through sh -c, so it is a command string, not a vector"},
	} {
		t.Run(tc.src, func(t *testing.T) { assertBins(t, tc.src, tc.want) })
	}
}

// TestNesting_NonShellInterpretersDoNotClusterTheirFlags pins the difference
// between a SHELL's `-c` and a `-c` that merely looks like one.
//
// A shell's short flags cluster: `-lc` sets both `l` and `c`, which is why the
// cluster test exists at all. `su` and `flock` are not shells — their `-c` is a
// long-style option that happens to be one letter, and neither accepts it
// packed into a cluster. Reading one out of `-lc` or `-nc` invents a spelling
// the program rejects.
//
// The cost of getting this wrong is not a miss but a FABRICATION, which is the
// worse direction: with clustering wrongly enabled, `flock /tmp/l -nc "npm
// publish"` reports a program whose name is the whole string `npm publish` —
// a binary nothing invokes and a basename no rule can match — and reports npm
// twice. Measured: nothing else in the suite noticed that.
func TestNesting_NonShellInterpretersDoNotClusterTheirFlags(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
		why  string
	}{
		// The exact `-c` is the payload flag and works.
		{`su -c "npm publish"`, []string{"su", "npm"}, "the exact -c is su's command option"},
		{`flock /tmp/l -c "npm publish"`, []string{"flock", "npm"}, "the exact -c is flock's command option"},

		// A cluster CONTAINING c is not. su has no such grammar, so there is
		// no command string here and the payload must not be read.
		{`su -lc "npm publish"`, []string{"su"}, "su does not accept -c packed into a cluster"},
		{`flock /tmp/l -nc "npm publish"`, []string{"flock"}, "flock does not either"},
	} {
		t.Run(tc.src, func(t *testing.T) {
			assertBins(t, tc.src, tc.want)
			// The fabrication check. The whole payload string arriving as one
			// program word is the specific damage clustering would do here.
			for _, inv := range ExtractCommand(tc.src).Invocations {
				if strings.Contains(inv.Bin, " ") {
					t.Errorf("bin %q contains a space — a command STRING was reported as a "+
						"single program word, which is a binary nothing invokes", inv.Bin)
				}
			}
		})
	}
}

// TestNesting_UserNamesAreNotReportedAsPrograms is the failure direction that
// adding `su` and `runuser` opens, and the reason each carries a rule saying
// its bare word is not a program.
//
// `su someuser` names a USER and starts an interactive shell. A plain wrapper
// entry reports a binary called someuser — an argument reported as a program,
// which the module calls the one outcome worse than missing it, and which
// would be worse here than the gap the entry was added to close.
func TestNesting_UserNamesAreNotReportedAsPrograms(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
	}{
		{`su someuser`, []string{"su"}},
		{`su - someuser`, []string{"su"}},
		{`su -l someuser`, []string{"su"}},
		{`su`, []string{"su"}},
		// The user trails the payload in su's real synopsis and is still not a
		// program.
		{`su -c "npm publish" someuser`, []string{"su", "npm"}},

		// runuser WITHOUT `-u` follows the same synopsis as su: the bare word
		// is the user. With `-u` the bare word is the program, which is the
		// other case and is pinned above.
		{`runuser someuser`, []string{"runuser"}},
		{`runuser - someuser`, []string{"runuser"}},
		{`runuser -l someuser`, []string{"runuser"}},
	} {
		t.Run(tc.src, func(t *testing.T) {
			assertBins(t, tc.src, tc.want)
			if contains(binsOf(tc.src), "someuser") {
				t.Errorf("ExtractCommand(%q) reports someuser as a program — a user name "+
					"promoted to a binary is the fabrication the module refuses", tc.src)
			}
		})
	}
}

// TestNesting_CommandDashVDescribesRatherThanRuns is the other over-reporting
// case that arrived with the builtins.
//
// `command -v npm` prints npm's path and runs nothing — verified against a real
// shell, where `command -v echo` prints `echo` and echoes nothing. Reporting
// npm there is a rule firing on a line that does not invoke it, and
// `command -v` is how nearly every install script probes for a tool, so the
// wrong answer would fire constantly.
func TestNesting_CommandDashVDescribesRatherThanRuns(t *testing.T) {
	for _, src := range []string{
		`command -v npm`,
		`command -V npm`,
	} {
		t.Run(src, func(t *testing.T) { assertBins(t, src, []string{"command"}) })
	}

	// Without the flag it really does run, and is reported.
	assertBins(t, `command npm publish`, []string{"command", "npm"})
}

// TestNesting_TimeoutConsumesItsDurationNotTheProgram.
//
// `timeout`'s synopsis is `timeout [OPTION] DURATION COMMAND [ARG]...` — a
// mandatory bare word before the command. Without knowing that, the unwrapper
// took the first non-flag word as the program: it reported a binary named `5`
// that nothing invokes AND lost npm entirely, which is both failure directions
// at once. The module's own comment calls an argument-reported-as-a-program
// "the one outcome worse than missing it".
//
// The wrapper vocabulary now carries a positional count, so the duration is
// spent and the real program is reported.
func TestNesting_TimeoutConsumesItsDurationNotTheProgram(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
	}{
		{`timeout 5 npm publish`, []string{"timeout", "npm"}},
		{`timeout 5s npm publish`, []string{"timeout", "npm"}},
		{`timeout 1.5h npm publish`, []string{"timeout", "npm"}},
		{`timeout infinity npm publish`, []string{"timeout", "npm"}},

		// The duration comes after the options, in every spelling of them.
		{`timeout -k 1 5 npm publish`, []string{"timeout", "npm"}},
		{`timeout --signal=TERM 10 npm publish`, []string{"timeout", "npm"}},
		{`timeout -s TERM 10 npm publish`, []string{"timeout", "npm"}},
		{`timeout --preserve-status 10 npm publish`, []string{"timeout", "npm"}},

		// `--` ends the OPTIONS, not the arguments — the duration still comes
		// before the command.
		{`timeout -- 5 npm publish`, []string{"timeout", "npm"}},

		// Stacked with other wrappers, in both orders.
		{`sudo timeout 5 npm publish`, []string{"sudo", "timeout", "npm"}},
		{`timeout 5 sudo npm publish`, []string{"timeout", "sudo", "npm"}},
		{`timeout 5 env FOO=1 npm publish`, []string{"timeout", "env", "npm"}},
	} {
		t.Run(tc.src, func(t *testing.T) {
			assertBins(t, tc.src, tc.want)
			if contains(binsOf(tc.src), "5") {
				t.Errorf("bins = %v — the duration is being reported as a program", binsOf(tc.src))
			}
		})
	}

	// A timeout with a duration and nothing after it wraps no program. The
	// duration must not be promoted to one just because it is the last word.
	assertBins(t, `timeout 5`, []string{"timeout"})
	assertBins(t, `timeout -k 1 5`, []string{"timeout"})

	// The wrapped vector starts at the program, so npm's argv does not carry
	// timeout's duration — a rule reading argv must not see an argument npm
	// never receives.
	invs := ExtractCommand(`timeout 5 npm publish`).Invocations
	if !equal(invs[1].Argv, []string{"npm", "publish"}) {
		t.Errorf("npm argv = %v, want [npm publish] without the duration", invs[1].Argv)
	}
}

// TestNesting_APositionalCountPastTheEndOfTheVectorDoesNotAbandonTheLine pins
// the one place the positional count can point past the vector it is counting
// into, and it is the `--` path that gets it there.
//
// `timeout --` is two words. The `--` is at index 1, so the command begins at
// index 2 — the end — and the wrapper's own positional is still owed, which
// asks for index 3 of a two-element slice. `rest`'s bounds check is what turns
// that into "no wrapped program" instead of a slice-out-of-range panic.
//
// The panic would not surface as a crash, which is what makes this worth a test
// of its own rather than trusting the guard's shape. `walk` recovers, KEEPS
// whatever it collected before the panic, and returns it as a complete answer —
// so the failure is silent and total for everything after the offending
// statement. With the guard removed, `timeout -- ; rm -rf /` reports NO
// invocations at all: not the timeout, and not the rm. A rule about `rm -rf`
// does not fire, and nothing anywhere says why.
//
// That is the exact shape the recover's own comment calls the worse outcome —
// "half a flattened list still lets a rule fire, where none lets it silently
// pass" — reached by a route the recover cannot distinguish from a parser bug.
// So the assertion is on the SIBLING statements surviving, not merely on the
// wrapper being reported: a guard that returned early without the bounds check
// would still report `timeout` while losing everything downstream.
//
// Every wrapper with a positional count meets this, so the boundary is walked
// rather than spot-checked: `--` at the end, `--` with fewer words after it
// than the count owes, and the same under a leading flag.
func TestNesting_APositionalCountPastTheEndOfTheVectorDoesNotAbandonTheLine(t *testing.T) {
	// The wrapper alone. It wraps nothing, and that is the whole answer.
	for _, src := range []string{
		`timeout --`,
		`timeout -k 1 --`,
		`timeout -s KILL --`,
	} {
		t.Run(src, func(t *testing.T) {
			assertBins(t, src, []string{"timeout"})
		})
	}

	// The load-bearing half: a statement AFTER the overshoot still gets
	// reported. This is what a bare `assertBins(t, "timeout --", ...)` cannot
	// see, because a panic mid-walk leaves the earlier invocations in place and
	// only silences what had not been reached yet.
	for _, tc := range []struct {
		src  string
		want []string
	}{
		{`timeout -- ; rm -rf /tmp/x`, []string{"timeout", "rm"}},
		{`timeout -- && npm publish`, []string{"timeout", "npm"}},
		{`timeout -k 1 -- ; npm publish`, []string{"timeout", "npm"}},
		// The overshoot inside a nested wrapper must not take the outer one's
		// siblings with it either.
		{`sudo timeout -- ; npm publish`, []string{"sudo", "timeout", "npm"}},
	} {
		t.Run(tc.src, func(t *testing.T) {
			assertBins(t, tc.src, tc.want)
		})
	}
}

// TestNesting_NiceTakesNoBarePositional is the other half of the same decision,
// and the reason the vocabulary is a per-wrapper count rather than a rule about
// number-shaped words.
//
// `nice`'s synopsis is `nice [-n increment] utility` — the increment ONLY comes
// behind a flag. So `nice 10 npm publish` genuinely does try to run a program
// called `10`, and reporting that is correct rather than a fabrication. A
// heuristic that skipped number-shaped words would get this wrong in the
// opposite direction, silently dropping a real invocation.
func TestNesting_NiceTakesNoBarePositional(t *testing.T) {
	assertBins(t, `nice 10 npm publish`, []string{"nice", "10"})

	// Behind its flag the increment is consumed, in both spellings.
	assertBins(t, `nice -n 5 npm publish`, []string{"nice", "npm"})
	assertBins(t, `nice --adjustment=5 npm publish`, []string{"nice", "npm"})

	// The legacy `nice -5 cmd` form is a flag, so it is skipped as one.
	assertBins(t, `nice -5 npm publish`, []string{"nice", "npm"})
}

// TestNesting_ClusteredWrapperFlagCarriesItsOwnValue.
//
// A short flag can carry its value with no space: `sudo -uroot npm publish` is
// `sudo -u root npm publish`, and `sudo -unpm publish` runs `publish` as user
// `npm`.
//
// This needs no special case and never did — a clustered word is not in
// takesValue, so the lookup already declines to eat the next word, which is the
// correct answer for exactly the right reason. My first report called this a
// bug; it was a misreading, and these cases are here to hold the behaviour
// rather than to mark a fix.
func TestNesting_ClusteredWrapperFlagCarriesItsOwnValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		// `-unpm` is `-u npm`, so `publish` is what sudo runs.
		{"clustered user then program", `sudo -unpm publish`, []string{"sudo", "publish"}},
		{"clustered user then npm", `sudo -uroot npm publish`, []string{"sudo", "npm"}},
		{"the evasion spelling", `sudo -unpm npm publish`, []string{"sudo", "npm"}},

		// The separated and inline spellings already worked and must keep
		// working — all three are the same command.
		{"separated user", `sudo -u root npm publish`, []string{"sudo", "npm"}},
		{"inline long user", `sudo --user=root npm publish`, []string{"sudo", "npm"}},

		// A long flag does NOT cluster: `--userroot` is not `--user root` to any
		// getopt, so it stays a valueless flag and the next word is the program.
		{"long flag does not cluster", `sudo --userroot npm publish`, []string{"sudo", "npm"}},

		// Other wrappers with value-taking short flags, same three spellings.
		{"xargs clustered -n", `xargs -n1 npm publish`, []string{"xargs", "npm"}},
		{"xargs separated -n", `xargs -n 1 npm publish`, []string{"xargs", "npm"}},
		{"xargs clustered -I", `xargs -I{} npm publish`, []string{"xargs", "npm"}},
		{"ionice clustered", `ionice -c3 npm publish`, []string{"ionice", "npm"}},
		{"nice clustered", `nice -n5 npm publish`, []string{"nice", "npm"}},
		{"env clustered unset", `env -uPATH npm publish`, []string{"env", "npm"}},
		{"timeout clustered signal", `timeout -sTERM 5 npm publish`, []string{"timeout", "npm"}},

		// A short flag that takes no value never clusters — `-E` is not a
		// prefix of anything, so `-Enpm` is one unknown valueless flag and the
		// program is still the next word.
		{"valueless short flag is not a value carrier", `sudo -E npm publish`, []string{"sudo", "npm"}},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBins(t, tc.src, tc.want) })
	}
}

// TestNesting_HeredocBodiesAreNotParsed: a heredoc body is data. `sh <<EOF` with
// `npm publish` inside runs npm, and only sh is reported.
//
// Right for `cat`, where the body is genuinely text. Arguably wrong for `sh`,
// where the body is a script — but distinguishing them means knowing which
// programs interpret stdin, which is the same open-ended question as the
// interpreter payload above. Pinned as current behaviour.
func TestNesting_HeredocBodiesAreNotParsed(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		{"cat heredoc", "cat <<EOF\nnpm publish\nEOF", []string{"cat"}},
		{"sh heredoc", "sh <<EOF\nnpm publish\nEOF", []string{"sh"}},
		{"quoted heredoc", "cat <<'EOF'\nnpm publish\nEOF", []string{"cat"}},
		{"heredoc with dash", "cat <<-EOF\n\tnpm publish\nEOF", []string{"cat"}},

		// An unquoted heredoc's body IS expanded by the shell, so a substitution
		// inside it really does run — and it is reported. The distinction the
		// module draws is between text (not walked) and syntax (walked), and a
		// substitution in an unquoted body is syntax.
		{"substitution inside an unquoted heredoc", "cat <<EOF\n$(npm publish)\nEOF", []string{"cat", "npm"}},

		// Inside a single-quoted delimiter nothing expands, so nothing runs and
		// nothing is reported. Correct.
		{"substitution inside a quoted heredoc", "cat <<'EOF'\n$(npm publish)\nEOF", []string{"cat"}},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBins(t, tc.src, tc.want) })
	}
}

// TestNesting_Redirections: a redirect is not a program and does not stop one
// being seen. A line that is only redirections runs nothing.
func TestNesting_Redirections(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		{"stdout", `npm publish > out.txt`, []string{"npm"}},
		{"stderr", `npm publish 2>/dev/null`, []string{"npm"}},
		{"dup", `npm publish >&2`, []string{"npm"}},
		{"append", `npm publish >> log`, []string{"npm"}},
		{"input", `npm publish < in.txt`, []string{"npm"}},
		{"leading redirect", `> out.txt npm publish`, []string{"npm"}},
		{"redirect only", `> out.txt`, []string{}},
		{"dup only", `2>&1`, []string{}},
		{"redirect in a loop", `while read -r l; do echo "$l"; done < <(npm publish)`, []string{"read", "echo", "npm"}},
	} {
		t.Run(tc.name, func(t *testing.T) { assertBins(t, tc.src, tc.want) })
	}
}

// TestNesting_DeepStructureIsFlatCompletely: depth is not a budget that runs
// out. A hundred wrappers and a hundred subshells both report everything.
func TestNesting_DeepStructureIsFlatCompletely(t *testing.T) {
	t.Run("100 nested subshells", func(t *testing.T) {
		src := strings.Repeat("( ", 100) + "npm publish" + strings.Repeat(" )", 100)
		assertBins(t, src, []string{"npm"})
	})

	t.Run("100 nested command substitutions", func(t *testing.T) {
		src := strings.Repeat("$(", 100) + "npm publish" + strings.Repeat(")", 100)
		if got := binsOf(src); !contains(got, "npm") {
			t.Errorf("bins = %v, want npm found at depth 100", got)
		}
	})

	t.Run("100 stacked wrappers", func(t *testing.T) {
		got := binsOf(strings.Repeat("sudo ", 100) + "npm publish")
		if len(got) != 101 || got[100] != "npm" {
			t.Errorf("got %d bins, last = %q; want 101 with npm last", len(got), got[len(got)-1])
		}
	})

	t.Run("100 chained statements", func(t *testing.T) {
		got := binsOf(strings.Repeat("npm publish && ", 99) + "npm publish")
		if len(got) != 100 {
			t.Errorf("got %d bins, want 100", len(got))
		}
	})

	// Deeply nested control flow, which is a different traversal path from
	// subshells: each level is a compound statement, not a parenthesised one.
	t.Run("50 nested if statements", func(t *testing.T) {
		src := strings.Repeat("if true; then ", 50) + "npm publish" + strings.Repeat("; fi", 50)
		if got := binsOf(src); !contains(got, "npm") {
			t.Errorf("bins = %v, want npm found under 50 levels of if", got)
		}
	})
}
