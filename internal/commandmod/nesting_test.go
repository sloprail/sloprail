package commandmod

import (
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
		// them alone would keep.
		{"mixed chain", `sudo sh -c 'x' && (time npm publish | tee log)`, []string{"sudo", "sh", "npm", "tee"}},
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

// TestNesting_InterpreterPayloadsAreOpaque documents the largest gap in this
// module, as behaviour rather than as a comment.
//
// `sh -c "npm publish"` runs npm. The string is an argument to sh, and this
// module does not re-parse it, so a rule matching `.bin == "npm"` does not fire
// on a line that publishes. The module's own doc calls this the resolution
// floor for `eval`, where the argument is only decided at runtime; for a
// literal `-c` string it is not undecidable at all — the text is right there.
//
// This test asserts the CURRENT behaviour so the gap is visible and so closing
// it is a deliberate change that turns these cases red rather than a silent
// widening. Owned by the `unwrap-interpreter-payloads` task in the strategy
// backlog, which argues the literalness line these cases are sorted by.
func TestNesting_InterpreterPayloadsAreOpaque(t *testing.T) {
	for _, tc := range []struct {
		name     string
		src      string
		want     []string
		unseen   string // the program that really runs and is not reported
		decided  bool   // true when the payload genuinely cannot be known statically
		whyRight string
	}{
		{
			name: "eval", src: `eval "npm publish"`, want: []string{"eval"}, unseen: "npm",
			decided:  true,
			whyRight: "eval's argument is re-interpreted at runtime after expansion; re-parsing has no bottom",
		},
		{
			name: "sh -c literal", src: `sh -c "npm publish"`, want: []string{"sh"}, unseen: "npm",
			decided:  false,
			whyRight: "the payload is a literal string and is statically readable; not re-parsing it is a gap",
		},
		{
			name: "bash -c literal", src: `bash -c "npm publish"`, want: []string{"bash"}, unseen: "npm",
			decided: false,
		},
		{
			name: "bash -lc chain", src: `bash -lc 'cd /x && npm publish'`, want: []string{"bash"}, unseen: "npm",
			decided: false,
		},
		{
			name: "zsh -c literal", src: `zsh -c 'npm publish'`, want: []string{"zsh"}, unseen: "npm",
			decided: false,
		},
		{
			name: "sh -c behind a wrapper", src: `sudo sh -c 'npm publish'`, want: []string{"sudo", "sh"}, unseen: "npm",
			decided: false,
		},
		{
			name: "xargs sh -c", src: `xargs sh -c 'npm publish'`, want: []string{"xargs", "sh"}, unseen: "npm",
			decided: false,
		},
		{
			name: "sh -c from a variable", src: `sh -c "$CMD"`, want: []string{"sh"}, unseen: "npm",
			decided:  true,
			whyRight: "the payload is a parameter; its value is not knowable without the runtime environment",
		},
		{
			name: "base64 decoded and piped to sh", src: `echo cm0gLXJmIC8= | base64 -d | sh`,
			want: []string{"echo", "base64", "sh"}, unseen: "rm",
			decided:  true,
			whyRight: "the decoded payload does not exist until base64 runs",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertBins(t, tc.src, tc.want)
			if contains(binsOf(tc.src), tc.unseen) {
				t.Errorf("ExtractCommand(%q) now reports %q — the gap this pins is closed; "+
					"update the case rather than deleting it", tc.src, tc.unseen)
			}
		})
	}
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
		`setsid npm publish`,
		`unbuffer npm publish`,
		`watch npm publish`,
		`command npm publish`,
		`exec npm publish`,
		`builtin npm publish`,
		`chroot / npm publish`,
		`su -c "npm publish"`,
		`runuser -u x npm publish`,
		`flock /tmp/l npm publish`,
		`torify npm publish`,
		`proxychains npm publish`,
	} {
		t.Run(src, func(t *testing.T) {
			if got := binsOf(src); contains(got, "npm") {
				t.Errorf("ExtractCommand(%q) bins = %v — %q is now unwrapped; that is an "+
					"improvement, so move this case to TestNesting_Wrappers", src, got, strings.Fields(src)[0])
			}
		})
	}
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
