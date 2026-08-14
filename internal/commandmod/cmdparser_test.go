package commandmod

import (
	"strings"
	"testing"
)

// This file is the command-parser corpus for the cases the other suites leave
// open: the wrapper/interpreter SEAM, the `--` terminator on the payload path,
// the file-target side of nesting, and the pathological inputs.
//
// It is organised by the question each group answers rather than by which
// source file the answer lives in, because the parser's failures are seams —
// two tables that each behave correctly and disagree with each other.
//
// The two BUGS this file found are at the top, each pinned by the smallest case
// that shows it, because a regression on either is silent: one loses a file
// rule entirely, the other invents a program name.

// ---------------------------------------------------------------------------
// BUG 1 — the file side never re-parses an interpreter payload
// ---------------------------------------------------------------------------

// TestFileTargets_AnInterpreterPayloadDoesNotHideTheFile is the file-side half
// of the gap TestNesting_LiteralInterpreterPayloadsAreUnwrapped closed for
// invocations, and it was still open.
//
// `sh -c 'rm notes.md'` reports `rm` in the INVOCATION list and reported NO
// file target at all. The two halves of this package disagreed about the same
// command line: a rule about running rm fired, and a rule about deleting
// notes.md did not.
//
// That is the exact failure the interpreter unwrapping exists to prevent,
// pointed at the other rule. targetsForArgv unwraps WRAPPERS — so `sudo rm
// notes.md` is caught, pinned by TestFileTargets_AWrapperDoesNotHideTheFile —
// but it never reached fromPayload, so an interpreter was a hole straight
// through every file rule. A harness that wraps everything in `bash -lc`, which
// is the case the depth bound's own doc comment says this module exists for,
// made every file rule cover nothing.
//
// The direction matters and is why this is a bug rather than a floor: the
// payload is LITERAL, right there in the text, and the invocation half already
// proves the module is willing to read it. Declining on the file side was not a
// certainty judgement, it was an omission.
func TestFileTargets_AnInterpreterPayloadDoesNotHideTheFile(t *testing.T) {
	// The plain case. Both halves must agree that notes.md is about to go.
	check(t, `sh -c 'rm notes.md'`, "remove:notes.md")
	check(t, `bash -c 'rm notes.md'`, "remove:notes.md")

	// The clustered spelling, which is how a harness actually writes it and so
	// is the case that made this matter.
	check(t, `bash -lc 'rm notes.md'`, "remove:notes.md")
	check(t, `sh -euxc 'rm notes.md'`, "remove:notes.md")

	// Every effect in the table, not just Remove — the payload is re-read as a
	// whole command line, so what it names is whatever the table says.
	check(t, `sh -c 'mv a.md b.md'`, "remove:a.md write:b.md")
	check(t, `sh -c 'touch new.md'`, "write:new.md")
	check(t, `sh -c 'sed -i "" s/a/b/ f.md'`, "write:f.md")

	// A REDIRECTION inside the payload. This is the half with no vocabulary,
	// and it was equally invisible: the payload was never parsed, so the `>`
	// inside it was never a Stmt anybody walked.
	check(t, `sh -c 'echo x > out.md'`, "write:out.md")
	check(t, `sh -c 'echo x >> out.md'`, "write:out.md")

	// The payload is a whole command line, so several files in one payload all
	// come back.
	check(t, `sh -c 'rm a.md && rm b.md'`, "remove:a.md remove:b.md")
	check(t, `sh -c 'rm a.md; echo x > b.md'`, "remove:a.md write:b.md")

	// Composed with the wrapper table in both directions, which is the seam
	// that made the gap invisible: the wrapper half worked, so `sudo rm` was
	// caught and `sudo sh -c 'rm'` was not.
	check(t, `sudo sh -c 'rm notes.md'`, "remove:notes.md")
	check(t, `sudo -u root bash -lc 'rm notes.md'`, "remove:notes.md")
	check(t, `timeout 5 sh -c 'rm notes.md'`, "remove:notes.md")
	check(t, `sh -c 'sudo rm notes.md'`, "remove:notes.md")

	// The non-shell interpreters that carry a command string are the same case.
	check(t, `su -c 'rm notes.md'`, "remove:notes.md")
	check(t, `flock /tmp/l -c 'rm notes.md'`, "remove:notes.md")

	// A path-spelled interpreter is the same interpreter here too, or `/bin/sh
	// -c` is a hole in exactly the way `/bin/rm` is not.
	check(t, `/bin/sh -c 'rm notes.md'`, "remove:notes.md")
}

// TestFileTargets_PayloadCertaintyMatchesTheInvocationSide pins the OTHER
// direction of the same fix, which is the half a fix could easily get wrong.
//
// Unwrapping the payload must not start guessing. The literalness precondition
// that governs the invocation side governs this one, because it is the same
// payload word being judged — a fix that re-parsed every payload would report
// files out of a string the environment decides.
//
// A note on what this does and does not kill, so the value is not overstated.
// The literalness and depth checks live in interpreterPayload, which BOTH
// halves call, so the invocation-side suite already kills a mutant that removes
// them — measured: deleting the literal test is killed by
// TestNesting_InterpreterPayloadsAreOpaque whether or not this test exists.
// What this adds is the file-specific half: that the payload's paths go through
// targetsFor and so inherit the empty-path and GLOB drops. That one it kills
// alone — measured, bypassing the glob check with only this test running fails
// it.
func TestFileTargets_PayloadCertaintyMatchesTheInvocationSide(t *testing.T) {
	// Not literal: the value is a runtime parameter. Nothing may be reported.
	check(t, `sh -c "$CMD"`, "(nothing)")
	// Resolves to `rm notes.md` under the empty environment and is still a
	// guess — resolve's `np${X}m` hazard, one layer down.
	check(t, `sh -c "r${X}m notes.md"`, "(nothing)")
	check(t, `sh -c "rm notes${X}.md"`, "(nothing)")
	// The payload does not exist until the substitution runs.
	check(t, `sh -c "$(cat run.sh)"`, "(nothing)")

	// A script FILE names no payload: its contents are not in the line.
	check(t, `sh deploy.sh`, "(nothing)")
	// `-s` reads the script from stdin; the string is $0, not code.
	check(t, `sh -s 'rm notes.md'`, "(nothing)")
	// A payload in another language is not re-parsed as shell.
	check(t, `python -c "import os; os.system('rm notes.md')"`, "(nothing)")
	// eval's argument is re-interpreted at runtime; re-parsing has no bottom.
	check(t, `eval "rm notes.md"`, "(nothing)")

	// A glob inside a payload is as unknowable as one outside it — the drop
	// happens in targetsFor, which the payload path must reach rather than
	// bypass.
	check(t, `sh -c 'rm *.md'`, "(nothing)")

	// An unparseable payload loses the payload only, and the outer line's own
	// redirection still reports.
	check(t, `sh -c 'if [' > out.md`, "write:out.md")
}

// TestFileTargets_PayloadDepthIsBoundedLikeTheInvocationSide pins that the
// payload budget governs this path too.
//
// The bound exists because each level is a fresh parse of attacker-shaped text
// and unbounded descent is quadratic in a string somebody else chose the length
// of. A file-side unwrapping that recursed without the budget would reintroduce
// exactly the hang the constant exists to prevent, on a path with its own
// entry point.
func TestFileTargets_PayloadDepthIsBoundedLikeTheInvocationSide(t *testing.T) {
	nest := func(n int) string {
		src := `rm notes.md`
		for i := 0; i < n; i++ {
			src = `sh -c ` + squote(src)
		}
		return src
	}

	// Inside the budget the file is found at every level.
	for n := 1; n <= maxUnwrapDepth; n++ {
		if got := render(FileTargets(nest(n))); got != "remove:notes.md" {
			t.Errorf("FileTargets at depth %d = %s, want remove:notes.md", n, got)
		}
	}

	// Past it the innermost payload is unread — the same shortfall the
	// invocation side takes, in the same direction.
	if got := render(FileTargets(nest(maxUnwrapDepth + 1))); got != "(nothing)" {
		t.Errorf("FileTargets at depth %d = %s, want nothing past the bound",
			maxUnwrapDepth+1, got)
	}

	// A wrapper is not a new parse and must spend none of the budget, or five
	// sudos in front of an `sh -c` would lose the file.
	src := strings.Repeat("sudo ", 20) + `sh -c 'rm notes.md'`
	if got := render(FileTargets(src)); got != "remove:notes.md" {
		t.Errorf("FileTargets(%q) = %s, want remove:notes.md — a wrapper must not "+
			"spend the payload budget", src, got)
	}
}

// ---------------------------------------------------------------------------
// BUG 2 — `--` after `-c` fabricates a program and loses the payload
// ---------------------------------------------------------------------------

// TestNesting_ADoubleDashAfterTheCommandFlagStillNamesThePayload pins a bug
// that fails in BOTH directions at once, which is the combination the module
// calls worst.
//
// `sh -c -- "npm publish"` reported a program named `--` and did NOT report
// npm. So a rule about npm silently never fired, and a rule matching on a bin
// would see a binary that cannot exist.
//
// Verified against real shells rather than reasoned about:
//
//	/bin/sh   -c -- "echo HI"           prints HI
//	/bin/bash -c -- "echo HI"           prints HI
//	/bin/sh   -c -- 'echo zero=$0' A B  prints zero=A one=B
//
// So the payload genuinely runs: after `-c`, a `--` is consumed as the option
// terminator and the NEXT word is still the command string. The old code
// returned `"", false` the moment it saw a `--`, whatever had come before it,
// so the payload went unread and the `--` fell through to the wrapper path and
// was promoted to a program.
//
// The asymmetry with `sh -- -c "npm publish"` is the whole point and is why the
// fix is narrow. There the `--` comes BEFORE any `-c`, so options really are
// over and the `-c` is a script FILENAME — real sh answers
// `sh: -c: No such file or directory`. That case must keep reporting sh alone,
// and it is pinned below alongside this one so the two cannot be conflated.
func TestNesting_ADoubleDashAfterTheCommandFlagStillNamesThePayload(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
		why  string
	}{
		// The payload runs, so it is read. `--` is the option terminator and is
		// not a program.
		{`sh -c -- "npm publish"`, []string{"sh", "npm"},
			"real sh runs the payload; -- is the terminator, not $0's program"},
		{`bash -c -- "npm publish"`, []string{"bash", "npm"}, "same for bash"},
		{`bash -lc -- "npm publish"`, []string{"bash", "npm"},
			"the clustered spelling reaches the same place"},
		{`zsh -c -- 'npm publish'`, []string{"zsh", "npm"}, "same for zsh"},

		// The non-shell interpreters take the exact `-c` and the same
		// terminator.
		{`su -c -- "npm publish"`, []string{"su", "npm"}, "su's -c, then the terminator"},
		{`flock /tmp/l -c -- "npm publish"`, []string{"flock", "npm"},
			"flock's lock file is spent as a positional, then -c, then the terminator"},

		// A flag between the two is still just a flag.
		{`bash -x -c -- "npm publish"`, []string{"bash", "npm"}, "-x is boolean and skipped"},

		// Behind a wrapper, so the fix composes rather than living only on the
		// top-level path.
		{`sudo sh -c -- "npm publish"`, []string{"sudo", "sh", "npm"}, "composes with the wrapper table"},
	} {
		t.Run(tc.src, func(t *testing.T) {
			assertBins(t, tc.src, tc.want)
			// The fabrication half, asserted separately from the list so a
			// regression says which failure it is. A program named `--` is a
			// binary nothing invokes.
			if contains(binsOf(tc.src), "--") {
				t.Errorf("ExtractCommand(%q) reports %q as a program — a terminator "+
					"promoted to a binary is the fabrication the module refuses: %s",
					tc.src, "--", tc.why)
			}
		})
	}

	// The OTHER side, which must not move. `--` BEFORE any `-c` really does end
	// the options, so the `-c` after it is a script filename and nothing runs.
	// These already passed and are restated here because the fix above is one
	// `if` away from breaking them, and the two readings are told apart only by
	// whether `-c` was already seen.
	for _, src := range []string{
		`sh -- -c "npm publish"`,
		`bash -- -lc "npm publish"`,
		`sh -x -- -c "npm publish"`,
	} {
		t.Run("terminator first: "+src, func(t *testing.T) {
			if got := binsOf(src); contains(got, "npm") {
				t.Errorf("ExtractCommand(%q) bins = %v — the -- came BEFORE any -c, so "+
					"options are over and the -c is a script FILENAME. Real sh answers "+
					"`-c: No such file or directory`; reporting npm fires a rule on a "+
					"line that runs nothing", src, got)
			}
		})
	}

	// A second `--` is the PAYLOAD, not a second terminator.
	//
	// Measured, and the answer is not the one a first reading suggests:
	//
	//	$ /bin/sh -c -- -- 'echo HI'
	//	echo HI: --: command not found
	//	exit=127
	//
	// So the shell consumed ONE terminator, took the second `--` as the command
	// string, and `'echo HI'` became $0. The payload is the literal text `--`,
	// which re-parses to a call whose program word is `--` — and the shell
	// genuinely tries to execute it, which is what the 127 says.
	//
	// Reporting `--` as an invocation is therefore CORRECT rather than the
	// fabrication the case above rejects, and the difference is worth stating
	// because the same two characters mean opposite things one position apart.
	// There, `--` was an option terminator the shell consumed and no process was
	// ever named; here it is the command string, and a program by that name is
	// what the line attempts. Whether such a program EXISTS is not a question a
	// static reader answers — the same judgement that makes `nice 10` report a
	// program called `10` and `builtin npm publish` report npm.
	//
	// What must NOT happen is the payload being read as code: `npm publish` is
	// $0 here, not a command, so a rule about npm must not fire.
	t.Run("a second terminator is the payload itself", func(t *testing.T) {
		got := binsOf(`sh -c -- -- "npm publish"`)
		if contains(got, "npm") {
			t.Errorf("bins = %v — the second -- is the command STRING (real sh answers "+
				"`--: command not found`), so what follows is $0 and not code", got)
		}
		if !equal(got, []string{"sh", "--"}) {
			t.Errorf("bins = %v, want [sh --] — the payload is the literal text `--`, "+
				"which the shell tries to execute and fails with 127", got)
		}
	})
}

// ---------------------------------------------------------------------------
// BUG 3 — a non-shell interpreter's own value-taking flag hid the payload
// ---------------------------------------------------------------------------

// TestNesting_AnInterpretersOwnFlagValueDoesNotHideThePayload pins a silent
// miss that applied to every value-taking option on a non-shell interpreter.
//
// `su -s /bin/sh -c "npm publish"` reported su ALONE. The payload went unread,
// so a rule about npm silently never fired on a line that runs it.
//
// The cause was two tables disagreeing. The scan skipped a flag's value only
// for interpreterFlagsTakingValue, which holds the SHELL-common options
// (`-o`, `--rcfile`). su's `-s`, su's `-g`, flock's `-w` and flock's `-E` were
// not in it, so their values — `/bin/sh`, `5` — arrived at the bare-word
// branch. A bare word means "this is the user name, options are over", the scan
// stopped there, and the `-c` behind it was never reached.
//
// The wrapper table already declared these same flags as value-taking for its
// own scan. So the two halves of the same program's vocabulary disagreed, and
// the interpreter half was the one that had not been told.
//
// A MISS rather than a fabrication, which is the safer of the two directions
// and is why it survived — nothing wrong was reported, a real thing was simply
// absent. But `su -s /bin/sh -c '...'` is an ordinary spelling, and a rule that
// never fires looks exactly like a rule being satisfied.
func TestNesting_AnInterpretersOwnFlagValueDoesNotHideThePayload(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
		why  string
	}{
		// su's shell and group options, both spellings.
		{`su -s /bin/sh -c "npm publish"`, []string{"su", "npm"}, "-s takes the shell path"},
		{`su --shell /bin/sh -c "npm publish"`, []string{"su", "npm"}, "the long spelling of -s"},
		{`su -g wheel -c "npm publish"`, []string{"su", "npm"}, "-g takes a group"},
		{`su -G extra -c "npm publish"`, []string{"su", "npm"}, "-G takes a supplementary group"},
		{`su -s /bin/sh -g wheel -c "npm publish"`, []string{"su", "npm"}, "several of them at once"},
		// With the user trailing the payload, which is su's real synopsis.
		{`su -s /bin/sh -c "npm publish" someuser`, []string{"su", "npm"}, "the trailing word is the user"},

		// flock's wait and exit-code options, whose values are bare NUMBERS —
		// the shape most likely to be read as a lock file.
		{`flock -w 5 /tmp/l -c "npm publish"`, []string{"flock", "npm"}, "-w takes a timeout"},
		{`flock --wait 5 /tmp/l -c "npm publish"`, []string{"flock", "npm"}, "the long spelling of -w"},
		{`flock --timeout 5 /tmp/l -c "npm publish"`, []string{"flock", "npm"}, "the other long spelling"},
		{`flock -E 9 /tmp/l -c "npm publish"`, []string{"flock", "npm"}, "-E takes an exit code"},
		{`flock -w 5 -E 9 /tmp/l -c "npm publish"`, []string{"flock", "npm"}, "both, then the lock file"},

		// runuser takes su's options and its own `-u`.
		{`runuser -s /bin/sh -c "npm publish" someuser`, []string{"runuser", "npm"}, "-s takes the shell"},
		{`runuser -g wheel -c "npm publish" someuser`, []string{"runuser", "npm"}, "-g takes a group"},
		{`runuser -u root -c "npm publish"`, []string{"runuser", "npm"}, "-u takes the user"},
	} {
		t.Run(tc.src, func(t *testing.T) {
			assertBins(t, tc.src, tc.want)
			if !contains(binsOf(tc.src), "npm") {
				t.Errorf("bins = %v — the payload went unread because %s and its value "+
					"was mistaken for a bare word, which ends the scan", binsOf(tc.src), tc.why)
			}
		})
	}

	// The file side reaches the same payload through its own descent, so the
	// fix has to hold on both or the two halves disagree again.
	check(t, `su -s /bin/sh -c 'rm notes.md'`, "remove:notes.md")
	check(t, `flock -w 5 /tmp/l -c 'rm notes.md'`, "remove:notes.md")

	// The flag's VALUE must not itself become a program. This is the
	// fabrication direction, and skipping a word is one off-by-one away from it.
	for _, tc := range []struct{ src, notBin string }{
		{`su -s /bin/sh -c "npm publish"`, "sh"},
		{`flock -w 5 /tmp/l -c "npm publish"`, "5"},
		{`flock -E 9 /tmp/l -c "npm publish"`, "9"},
		{`runuser -u root -c "npm publish"`, "root"},
	} {
		// `sh` is a real program name, so the check is that the option's value
		// did not become an INVOCATION of its own — the list is exactly the
		// interpreter and its payload.
		if got := binsOf(tc.src); len(got) != 2 {
			t.Errorf("ExtractCommand(%q) bins = %v, want exactly the interpreter and "+
				"its payload — an option's value must not be promoted to a program",
				tc.src, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Wrapper/interpreter seam — the programs that live in BOTH tables
// ---------------------------------------------------------------------------

// TestNesting_DualRoleProgramsPickTheRightTable pins `flock`, `su` and
// `runuser`, each of which is a wrapper AND an interpreter, and which of the
// two applies depends only on the spelling.
//
// The failure mode here is not a miss, it is a CATEGORY error: reading a
// command STRING as a vector reports one program whose name is the whole string
// — a basename no rule can match, containing a space. Reading a vector as a
// string loses everything in it.
func TestNesting_DualRoleProgramsPickTheRightTable(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
		why  string
	}{
		// flock, vector spelling: the lock path is a positional, the rest is an
		// argument vector.
		{`flock /tmp/l npm publish`, []string{"flock", "npm"}, "vector form: lock path then command"},
		{`flock -w 5 /tmp/l npm publish`, []string{"flock", "npm"}, "a value-taking flag before the lock path"},
		{`flock --timeout 5 /tmp/l npm publish`, []string{"flock", "npm"}, "the long spelling of the same"},
		{`flock -n /tmp/l npm publish`, []string{"flock", "npm"}, "a boolean flag before the lock path"},
		{`flock /tmp/l -- npm publish`, []string{"flock", "npm"}, "the terminator, then the vector"},

		// flock, string spelling: `-c` makes the next word a command string,
		// run through sh -c.
		{`flock /tmp/l -c "npm publish"`, []string{"flock", "npm"}, "string form: -c payload"},
		{`flock -w 5 /tmp/l -c "npm publish"`, []string{"flock", "npm"}, "flags, lock path, then -c"},

		// runuser, both synopses. With `-u` a bare word is the PROGRAM; without
		// it a bare word is the USER and must never be promoted.
		{`runuser -u root npm publish`, []string{"runuser", "npm"}, "with -u the bare word is the program"},
		{`runuser -u root -- npm publish`, []string{"runuser", "npm"}, "the terminator does not change that"},
		{`runuser --user root npm publish`, []string{"runuser", "npm"}, "the long spelling enables it too"},
		{`runuser -c "npm publish" root`, []string{"runuser", "npm"}, "string form: the trailing word is the user"},

		// su is wrapsNothing on the vector path, so only `-c` ever produces a
		// second invocation.
		{`su -c "npm publish"`, []string{"su", "npm"}, "the only shape su runs a command in"},
		{`su -s /bin/sh -c "npm publish"`, []string{"su", "npm"}, "-s takes a value and is not the payload"},
		{`su --session-command "npm publish"`, []string{"su"},
			"--session-command is a long option; no long option is read as the payload flag"},
	} {
		t.Run(tc.src, func(t *testing.T) {
			assertBins(t, tc.src, tc.want)
			// The category error, checked on every case: a command STRING
			// reported as one program word always contains a space.
			for _, inv := range ExtractCommand(tc.src).Invocations {
				if strings.Contains(inv.Bin, " ") {
					t.Errorf("bin %q contains a space — a command string was read as a "+
						"single program word (%s)", inv.Bin, tc.why)
				}
			}
		})
	}
}

// TestNesting_DualRoleProgramsNamingNothing pins the dual-role entries when
// there is no command at all. Each is genuinely invoked and each wraps nothing,
// so the answer is the wrapper alone — never a flag or a user promoted to fill
// the gap.
func TestNesting_DualRoleProgramsNamingNothing(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
	}{
		{`flock /tmp/l`, []string{"flock"}},
		{`flock -c`, []string{"flock"}},
		{`flock /tmp/l -c`, []string{"flock"}},
		{`su -c`, []string{"su"}},
		{`su -s /bin/sh`, []string{"su"}},
		{`runuser -u root`, []string{"runuser"}},
		{`runuser -c`, []string{"runuser"}},
		// A wrapper whose flag eats the last word leaves nothing behind it.
		{`sudo -u`, []string{"sudo"}},
		{`timeout -k`, []string{"timeout"}},
		{`xargs -I`, []string{"xargs"}},
		{`exec -a`, []string{"exec"}},
		{`env -u`, []string{"env"}},
		{`nice -n`, []string{"nice"}},
		// An interpreter whose payload flag is the last word names no payload.
		{`sh -c`, []string{"sh"}},
		{`bash -lc`, []string{"bash"}},
		{`bash -o pipefail`, []string{"bash"}},
	} {
		t.Run(tc.src, func(t *testing.T) {
			assertBins(t, tc.src, tc.want)
			if len(binsOf(tc.src)) != 1 {
				t.Errorf("bins = %v — nothing is wrapped here, so nothing may be invented",
					binsOf(tc.src))
			}
		})
	}
}

// TestNesting_AnEmptyPayloadNamesNoProgram pins the payload that is present,
// literal, and contains nothing to run.
//
// It is a distinct path from "no payload word at all": the word EXISTS and is
// certain, so every precondition passes and the re-parse happens — it just
// finds no call. An implementation that treated "parsed nothing" as a reason to
// fall through to another reading would invent a program here.
func TestNesting_AnEmptyPayloadNamesNoProgram(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
	}{
		{`sh -c ""`, []string{"sh"}},
		{`sh -c ''`, []string{"sh"}},
		{`sh -c " "`, []string{"sh"}},
		{`sh -c "   "`, []string{"sh"}},
		{`bash -lc ""`, []string{"bash"}},
		{`sh -c "# only a comment"`, []string{"sh"}},
		{`sh -c ";"`, []string{"sh"}},
		{`sh -c "&&"`, []string{"sh"}},
	} {
		t.Run(tc.src, func(t *testing.T) { assertBins(t, tc.src, tc.want) })
	}
}

// ---------------------------------------------------------------------------
// Wrappers that take positionals, flags with values, and terminators
// ---------------------------------------------------------------------------

// TestNesting_StackedWrappersOfEveryShape is the composition test the brief
// asks for: wrappers that take positionals, wrappers with value-taking flags,
// terminators and assignments, all in one stack.
//
// Each element is pinned alone elsewhere. What is new here is that they COMPOSE
// — the recursion hands the wrapped vector to a fresh scan, so a mistake in one
// entry's arithmetic shows up as the next wrapper's program word being wrong.
func TestNesting_StackedWrappersOfEveryShape(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
	}{
		// The brief's own example.
		{`sudo nohup timeout 5 rm x.md`, []string{"sudo", "nohup", "timeout", "rm"}},

		// A positional-taking wrapper in the middle of a stack: the duration
		// must be spent at the right depth, or the next wrapper's name is eaten.
		{`sudo timeout 5 nohup npm publish`, []string{"sudo", "timeout", "nohup", "npm"}},
		{`nohup timeout 5 sudo npm publish`, []string{"nohup", "timeout", "sudo", "npm"}},

		// Value-taking flags at several depths at once.
		{`sudo -u root timeout -k 1 5 nice -n 5 npm publish`,
			[]string{"sudo", "timeout", "nice", "npm"}},
		{`env FOO=1 sudo -u root nohup npm publish`, []string{"env", "sudo", "nohup", "npm"}},

		// Terminators at several depths. Each `--` ends only its OWN wrapper's
		// options.
		{`sudo -- timeout -- 5 npm publish`, []string{"sudo", "timeout", "npm"}},
		{`sudo -u root -- env -- npm publish`, []string{"sudo", "env", "npm"}},

		// Two positional-taking wrappers stacked, which is where an off-by-one
		// in the countdown becomes visible.
		{`timeout 5 flock /tmp/l npm publish`, []string{"timeout", "flock", "npm"}},
		{`flock /tmp/l timeout 5 npm publish`, []string{"flock", "timeout", "npm"}},
		{`chroot / timeout 5 npm publish`, []string{"chroot", "timeout", "npm"}},

		// An interpreter at the bottom of a wrapper stack, which is the shape a
		// harness actually produces.
		{`sudo nohup timeout 5 bash -lc 'npm publish'`,
			[]string{"sudo", "nohup", "timeout", "bash", "npm"}},
	} {
		t.Run(tc.src, func(t *testing.T) { assertBins(t, tc.src, tc.want) })
	}
}

// TestNesting_AWrapperNeverEatsTheProgramWithAValuelessFlag pins the direction
// a too-eager takesValue table fails in.
//
// A flag that does NOT take a value must leave the next word alone. If a
// boolean flag were listed as value-taking, it would swallow the program and
// the invocation would vanish — a silent miss with no symptom in the output.
func TestNesting_AWrapperNeverEatsTheProgramWithAValuelessFlag(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
	}{
		{`sudo -E npm publish`, []string{"sudo", "npm"}},
		{`sudo -H npm publish`, []string{"sudo", "npm"}},
		{`sudo -n npm publish`, []string{"sudo", "npm"}},
		{`sudo -b npm publish`, []string{"sudo", "npm"}},
		{`env -i npm publish`, []string{"env", "npm"}},
		{`env -0 npm publish`, []string{"env", "npm"}},
		{`timeout --preserve-status 5 npm publish`, []string{"timeout", "npm"}},
		{`timeout --foreground 5 npm publish`, []string{"timeout", "npm"}},
		{`xargs -r npm publish`, []string{"xargs", "npm"}},
		{`xargs -t npm publish`, []string{"xargs", "npm"}},
		{`nohup npm publish`, []string{"nohup", "npm"}},
		{`setsid -w npm publish`, []string{"setsid", "npm"}},
		{`unbuffer -p npm publish`, []string{"unbuffer", "npm"}},
		{`flock -n /tmp/l npm publish`, []string{"flock", "npm"}},
		{`flock -s /tmp/l npm publish`, []string{"flock", "npm"}},
	} {
		t.Run(tc.src, func(t *testing.T) {
			assertBins(t, tc.src, tc.want)
			if !contains(binsOf(tc.src), "npm") {
				t.Errorf("bins = %v — a valueless flag swallowed the program, which is a "+
					"silent miss: the invocation simply is not there", binsOf(tc.src))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Redirections — the operators, and the words that are not paths
// ---------------------------------------------------------------------------

// TestFileTargets_EveryWritingOperatorAndEveryReadingOne walks the redirection
// operator table exhaustively, because writesAFile enumerates POSITIVELY: an
// operator it has not heard of touches nothing, so a missing entry is a silent
// miss and a wrong entry is a rule firing on a file nothing writes.
//
// The reading half is the one that would be catastrophic in the other
// direction: if `<` produced a Write, every `grep foo < in.md` would fire a
// rule about modifying in.md.
func TestFileTargets_EveryWritingOperatorAndEveryReadingOne(t *testing.T) {
	t.Run("writing operators name their file", func(t *testing.T) {
		for _, tc := range []struct{ src, want string }{
			{`cmd > f.md`, "write:f.md"},
			{`cmd >> f.md`, "write:f.md"},
			{`cmd >| f.md`, "write:f.md"},
			{`cmd &> f.md`, "write:f.md"},
			{`cmd &>> f.md`, "write:f.md"},
			{`cmd <> f.md`, "write:f.md"},
			// A numbered descriptor still names a real file.
			{`cmd 2> f.md`, "write:f.md"},
			{`cmd 3> f.md`, "write:f.md"},
			{`cmd 2>> f.md`, "write:f.md"},
		} {
			check(t, tc.src, tc.want)
		}
	})

	t.Run("reading operators change nothing", func(t *testing.T) {
		for _, src := range []string{
			`cmd < f.md`,
			`cmd 0< f.md`,
			"cmd <<EOF\nf.md\nEOF",
			"cmd <<-EOF\n\tf.md\nEOF",
			"cmd <<'EOF'\nf.md\nEOF",
			`cmd <<< f.md`,
		} {
			if got := render(FileTargets(src)); got != "(nothing)" {
				t.Errorf("FileTargets(%q) = %s — a reading redirection must produce no "+
					"write, or every command with an input file fires a rule about "+
					"modifying it", src, got)
			}
		}
	})

	t.Run("descriptor words are not paths", func(t *testing.T) {
		// The word after a duplication is a NUMBER, and reporting it would
		// announce a file called "1" on the commonest idiom in shell.
		for _, src := range []string{
			`cmd 2>&1`,
			`cmd >&2`,
			`cmd 1>&2`,
			`cmd 2>&-`,
			`cmd <&0`,
			`cmd 0<&-`,
		} {
			if got := render(FileTargets(src)); got != "(nothing)" {
				t.Errorf("FileTargets(%q) = %s — the word is a file descriptor, not a path", src, got)
			}
			if contains(pathsOf(FileTargets(src)), "1") {
				t.Errorf("FileTargets(%q) reported a file named \"1\"", src)
			}
		}
		// A real file alongside a duplication: only the file.
		check(t, `cmd > out.log 2>&1`, "write:out.log")
		check(t, `cmd 2>&1 > out.log`, "write:out.log")
	})
}

// pathsOf is the path list of a target slice, for the assertions that ask
// whether a particular string was reported at all.
func pathsOf(targets []FileTarget) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.Path)
	}
	return out
}

// TestFileTargets_RedirectionsInsideNestedStructure pins that a redirection is
// found wherever the shell puts it, and that the file it names belongs to the
// statement it hangs off rather than to the line.
func TestFileTargets_RedirectionsInsideNestedStructure(t *testing.T) {
	check(t, `(cmd > inner.md)`, "write:inner.md")
	check(t, `{ cmd > inner.md; }`, "write:inner.md")
	check(t, `if true; then cmd > inner.md; fi`, "write:inner.md")
	check(t, `for f in a; do cmd > inner.md; done`, "write:inner.md")
	check(t, `while true; do cmd > inner.md; done`, "write:inner.md")
	// A redirection on the compound statement itself, not on the inner call.
	check(t, `{ cmd; } > outer.md`, "write:outer.md")
	check(t, `(cmd) > outer.md`, "write:outer.md")
	check(t, `for f in a; do cmd; done > outer.md`, "write:outer.md")
	// Both at once — two different statements, two different files.
	check(t, `{ cmd > inner.md; } > outer.md`, "write:inner.md write:outer.md")
	// In a pipeline, each stage keeps its own.
	check(t, `a > one.md | b > two.md`, "write:one.md write:two.md")
	// Backgrounded and negated statements still carry theirs.
	check(t, `cmd > f.md &`, "write:f.md")
	check(t, `! cmd > f.md`, "write:f.md")
	// Inside a command substitution.
	check(t, `echo $(cmd > f.md)`, "write:f.md")
}

// ---------------------------------------------------------------------------
// Paths that are not ordinary relative files
// ---------------------------------------------------------------------------

// TestFileTargets_PathShapesAreReportedAsWritten pins that this package does
// NOT resolve, clean, or judge a path — it reports what the line says, and
// where the repository sits is the file module's question.
//
// Worth pinning because every one of these is a shape somebody would be tempted
// to normalise here, and normalising in two places is how the two disagree.
func TestFileTargets_PathShapesAreReportedAsWritten(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		// Absolute, and outside any repository.
		{`rm /etc/passwd`, "remove:/etc/passwd"},
		{`rm /tmp/../etc/passwd`, "remove:/tmp/../etc/passwd"},
		// Traversal is not resolved away.
		{`rm ../../outside.md`, "remove:../../outside.md"},
		{`rm ./notes.md`, "remove:./notes.md"},
		{`rm a/../b.md`, "remove:a/../b.md"},
		// Redundant separators survive: cleaning is not this layer's job.
		{`rm a//b.md`, "remove:a//b.md"},
		// A directory operand is reported as named — whether the path is a
		// directory is a question about the TREE, which this package does not read.
		{`rm -rf build`, "remove:build"},
		{`rm -rf build/`, "remove:build/"},
		{`mkdir newdir`, "write:newdir"},
		// A dotfile and a path with spaces, quoted.
		{`rm .env`, "remove:.env"},
		{`rm "my notes.md"`, "remove:my notes.md"},
		{`rm 'my notes.md'`, "remove:my notes.md"},
		{`rm my\ notes.md`, "remove:my notes.md"},
		// A tilde is NOT resolved against whoever the guardrail runs as.
		{`rm ~/notes.md`, "remove:~/notes.md"},
		// A bare `-` is kept: it means stdin to most utilities and the file
		// module resolves it against the tree and finds nothing.
		{`rm -- -`, "remove:-"},
	} {
		check(t, tc.src, tc.want)
	}
}

// TestFileTargets_GlobsAreDroppedWhereverTheyAppear pins the measured behaviour
// the brief names: `rm *.md` produces NO event.
//
// This is a KNOWN COST, not a bug, and it is pinned so nobody "fixes" it into
// reporting a path that matches no file. safeConfig disables globbing so the
// filesystem is never read, which means the `*` survives expansion and the word
// looks literal — isLiteral agrees, because a `*` is a plain Lit. Reporting
// `*.md` would name a path no rule can mean and no file can match; the tree
// diff at session stop is what covers this tier.
func TestFileTargets_GlobsAreDroppedWhereverTheyAppear(t *testing.T) {
	// Each glob metacharacter, on an operand.
	check(t, `rm *.md`, "(nothing)")
	check(t, `rm notes.?`, "(nothing)")
	check(t, `rm notes[12].md`, "(nothing)")
	// On a redirection target, which is a second construction site and so a
	// second place the check could be forgotten.
	check(t, `echo x > *.md`, "(nothing)")
	check(t, `echo x >> logs/*.log`, "(nothing)")
	// On each operand position of a multi-operand binary.
	check(t, `mv *.md dest.md`, "write:dest.md")
	check(t, `mv a.md *.md`, "remove:a.md")
	check(t, `cp *.md dest.md`, "write:dest.md")
	// Behind a wrapper and inside a payload — the drop must not be bypassed by
	// a path that reaches targetsFor from somewhere else.
	check(t, `sudo rm *.md`, "(nothing)")
	// A glob alongside a certain path still reports the certain one, which is
	// what stops this being a blanket "any glob silences the line".
	check(t, `rm notes.md *.tmp`, "remove:notes.md")
	check(t, `rm *.tmp notes.md`, "remove:notes.md")

	// The invocation side does NOT drop the glob: it is an argument the command
	// genuinely receives, and only a PATH claim is unsafe. Pinning both sides
	// keeps the asymmetry deliberate.
	invs := ExtractCommand(`rm *.md`).Invocations
	if len(invs) != 1 || !equal(invs[0].Argv, []string{"rm", "*.md"}) {
		t.Errorf("argv = %v, want the glob preserved in argv even though it names no path",
			invs[0].Argv)
	}
}

// ---------------------------------------------------------------------------
// Measured behaviours the brief names — pinned so nobody "fixes" them
// ---------------------------------------------------------------------------

// TestParser_MeasuredBehavioursThatLookLikeBugsAndAreNot collects the answers
// two previous agents reported as defects and then retracted.
//
// Each is correct, each looks wrong at a glance, and each is one plausible
// "fix" away from being broken in the fabrication direction. They are pinned
// TOGETHER, with the argument attached, so the next reader finds the reasoning
// at the same time as the behaviour — the individual cases are pinned in their
// own suites, and what this adds is the shared explanation.
func TestParser_MeasuredBehavioursThatLookLikeBugsAndAreNot(t *testing.T) {
	t.Run("nice 10 reports a program named 10", func(t *testing.T) {
		// `nice`'s synopsis is `nice [-n increment] utility` — the increment
		// comes ONLY behind a flag. So `nice 10 npm publish` really does try to
		// run a program called `10`, and reporting it is correct. A heuristic
		// that skipped number-shaped words would drop a real invocation, and
		// would have to guess about `infinity` and `0.5s` too.
		assertBins(t, `nice 10 npm publish`, []string{"nice", "10"})
		// The contrast that proves the count is per-wrapper vocabulary and not
		// a shape test: timeout DOES take a bare duration, so the same-shaped
		// word is spent there.
		assertBins(t, `timeout 10 npm publish`, []string{"timeout", "npm"})
	})

	t.Run("timeout 5 reports npm and never a program named 5", func(t *testing.T) {
		assertBins(t, `timeout 5 npm publish`, []string{"timeout", "npm"})
		if contains(binsOf(`timeout 5 npm publish`), "5") {
			t.Error("the duration was promoted to a program")
		}
	})

	t.Run("sudo -unpm publish is not a bug", func(t *testing.T) {
		// `-unpm` is the clustered spelling of `-u npm`, so sudo runs `publish`
		// as user npm. Reporting `publish` is exactly right, and it needs no
		// special case: a clustered word is not a key in takesValue, so the
		// lookup declines to eat the next word for the correct reason.
		assertBins(t, `sudo -unpm publish`, []string{"sudo", "publish"})
		// The evasion spelling, where the real npm follows.
		assertBins(t, `sudo -unpm npm publish`, []string{"sudo", "npm"})
	})

	t.Run("rm with a glob produces no file event", func(t *testing.T) {
		check(t, `rm *.md`, "(nothing)")
		// Stated as the cost it is: a rule protecting notes.md does not fire
		// here, and the tree diff at session stop is the backstop.
		if got := render(FileTargets(`rm *.md`)); got != "(nothing)" {
			t.Errorf("got %s — a glob names a SET of paths, not one path; reporting "+
				"`*.md` would name a file no rule can mean and none can match", got)
		}
	})

	t.Run("command -v describes and does not run", func(t *testing.T) {
		assertBins(t, `command -v npm`, []string{"command"})
		assertBins(t, `command -V npm`, []string{"command"})
		// And the file side agrees: `command -v rm notes.md` deletes nothing.
		check(t, `command -v rm notes.md`, "(nothing)")
		// Without the flag it really runs, on both sides.
		assertBins(t, `command npm publish`, []string{"command", "npm"})
		check(t, `command rm notes.md`, "remove:notes.md")
	})

	t.Run("a wrapper that wraps nothing invents no program", func(t *testing.T) {
		assertBins(t, `su someuser`, []string{"su"})
		assertBins(t, `sudo`, []string{"sudo"})
		// The file side must not invent one either: `su someuser rm notes.md`
		// is su running an interactive shell as someuser, not an rm.
		check(t, `su someuser rm notes.md`, "(nothing)")
	})
}

// ---------------------------------------------------------------------------
// Pathological input
// ---------------------------------------------------------------------------

// TestParser_PathologicalInputNeverPanicsAndNeverInvents feeds both entry
// points the shapes an agent would choose to break a parser.
//
// The bar is not "returns the right answer" — for most of these there is no
// right answer — it is that the call RETURNS. A panic escaping either function
// exits the guardrail non-zero, and a harness reads that as a refusal: a parser
// bug would not weaken a rule, it would block the agent's work for a reason
// nobody can act on.
//
// FileTargets is included deliberately. It has its own parse, its own walk and
// its own recover, so a case that is safe through ExtractCommand proves nothing
// about it — and the payload re-parse this file added gives it a second entry
// into the parser that did not exist before.
func TestParser_PathologicalInputNeverPanicsAndNeverInvents(t *testing.T) {
	cases := map[string]string{
		"deeply nested payloads far past the bound": deepPayload(8),
		"deeply nested subshells":                   strings.Repeat("(", 200) + "rm x" + strings.Repeat(")", 200),
		"deeply nested substitutions":               strings.Repeat("$(", 200) + "rm x" + strings.Repeat(")", 200),
		"unbalanced open parens":                    strings.Repeat("(", 5000),
		"unbalanced close parens":                   strings.Repeat(")", 5000),
		"a very long single word":                   strings.Repeat("a", 500000),
		"a very long path operand":                  "rm " + strings.Repeat("a/", 100000) + "b.md",
		"a very long payload":                       `sh -c '` + strings.Repeat("rm x.md; ", 20000) + `'`,
		"many redirections":                         "cmd" + strings.Repeat(" > f.md", 10000),
		"many stacked wrappers":                     strings.Repeat("sudo ", 10000) + "rm x.md",
		"many quotes":                               strings.Repeat(`"`, 10000),
		"many backslashes":                          strings.Repeat(`\`, 10000),
		"invalid utf8":                              "rm \xff\xfe\xfd.md",
		"invalid utf8 in a payload":                 "sh -c 'rm \xff\xfe.md'",
		"NUL bytes":                                 "rm \x00 x.md",
		"NUL byte alone":                            "\x00",
		"mixed control characters":                  "rm \x01\x02\x03\x07\x1b[0m.md",
		"a lone carriage return":                    "\r",
		"unterminated everything":                   `sh -c "$( ' ` + "`",
		"only operators":                            `&& || ; | & ;; |& >`,
		"only redirect operators":                   `> >> < << <<< <> >| &> &>>`,
		"an empty line":                             ``,
		"only whitespace":                           " \t\n\v\f\r ",
		"a comment only":                            `# rm notes.md`,
		"a payload that is only a comment":          `sh -c '# rm notes.md'`,
		"heredoc with no terminator":                "sh <<EOF\nrm x.md\n",
		"a null-terminated payload":                 "sh -c 'rm x.md\x00rm y.md'",
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			// Both entry points must RETURN. Without the recovers these crash
			// the test binary rather than failing it.
			ev := ExtractCommand(src)
			targets := FileTargets(src)

			// The raw line always rides along, however unreadable — it is what
			// a rule about unreadable commands matches on.
			if ev.Raw != src {
				t.Errorf("raw was not preserved")
			}
			// Nothing incoherent may escape, whatever was collected.
			for _, inv := range ev.Invocations {
				if inv.Bin == "" {
					t.Errorf("an empty bin reached the list: a program no rule can mean")
				}
				if len(inv.Argv) == 0 {
					t.Errorf("an empty argv reached the list")
				}
				if inv.Flags == nil {
					t.Errorf("a nil flag map reached the list")
				}
				if _, ok := inv.Flags[""]; ok {
					t.Errorf("a flag keyed by the empty string reached the list")
				}
			}
			for _, target := range targets {
				if target.Path == "" {
					t.Errorf("an empty path reached the target list")
				}
				if hasGlob(target.Path) {
					t.Errorf("a glob %q reached the target list: it names a SET of paths", target.Path)
				}
			}
		})
	}
}

// deepPayload builds n interpreters of nesting, with the payload at each level
// quoted so it survives as ONE literal word.
//
// n is kept small on purpose, and the reason is a trap worth recording. Each
// level must escape the quotes of every level inside it, so the source grows
// roughly FOURFOLD per level: measured, depth 4 is 151 bytes, depth 10 is 87KB,
// and depth 12 is 797KB. A "deep" nesting test written with a big n is not
// testing the parser at all, it is building a string that dwarfs any real input
// — depth 200 does not fit in memory, let alone in a command line.
//
// So depth is expressed as a handful of levels past maxUnwrapDepth, which is
// what the bound is about, and the SIZE stress is a separate case built from a
// long flat payload instead.
func deepPayload(n int) string {
	src := `rm notes.md`
	for i := 0; i < n; i++ {
		src = `sh -c ` + squote(src)
	}
	return src
}

// TestParser_DeepNestingStopsDoingWorkAtTheBound is the HANG half of the depth
// bound, which the correctness assertions cannot see.
//
// TestNesting_InterpreterPayloadDepthIsBounded proves the innermost payload
// goes unread past the bound. It does not prove the WORK stopped: an
// implementation that walked every level and merely discarded the results would
// pass it and still be quadratic in a length the agent chose. The recover
// catches a panic but not a hang, and the module's own doc comment says a
// guardrail that hangs is as bad as one that crashes.
//
// Asserted as a bounded invocation COUNT rather than a wall-clock deadline,
// because a timing assertion on a shared CI box is a flake. One level is
// re-parsed per unit of budget, so the list length is a direct reading of how
// many parses happened: a run that reports more than maxUnwrapDepth+1
// interpreters has descended past where it says it stops.
func TestParser_DeepNestingStopsDoingWorkAtTheBound(t *testing.T) {
	// Well past the bound of 4, and still a 10KB input rather than an
	// impossible one — see deepPayload on why the depth is small.
	const depth = 8
	src := deepPayload(depth)

	got := binsOf(src)
	// Every level that WAS walked is an sh, and the most that may appear is the
	// outermost interpreter plus one per unit of budget. More than that means
	// levels past the bound were parsed and their results thrown away.
	if len(got) > maxUnwrapDepth+1 {
		t.Errorf("bins has %d entries for %d levels of nesting, want at most %d — "+
			"levels past the bound are still being parsed, which is the quadratic "+
			"descent the bound exists to prevent", len(got), depth, maxUnwrapDepth+1)
	}
	if contains(got, "rm") {
		t.Errorf("bins = %v — rm sits %d levels down, past the bound of %d",
			got, depth, maxUnwrapDepth)
	}
	// The file side has its own entry into the parser and its own descent, so
	// the same bound has to hold there independently.
	if targets := FileTargets(src); len(targets) != 0 {
		t.Errorf("FileTargets = %v, want nothing past the bound", targets)
	}

	// The SIZE stress, separated from the depth stress: one shallow payload
	// holding twenty thousand statements. This is the shape a long real command
	// line takes, and it must come back complete rather than truncated.
	long := `sh -c '` + strings.Repeat("rm x.md; ", 20000) + `'`
	if n := len(binsOf(long)); n != 20001 {
		t.Errorf("a 20000-statement payload reported %d invocations, want 20001 — "+
			"the sh plus every rm; a long payload must not be truncated", n)
	}
}
