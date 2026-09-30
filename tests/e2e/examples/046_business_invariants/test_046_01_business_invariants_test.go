package e2e

// TODO(D3): drive verdict via a10n-claude-mock once a10n-cli#470 lands + new mock
// on PATH; today InstallJudgeClaude supplies the verdict.
//
// Use case: business-invariants (unit 04 + its 02_pinned-spec-reference addendum).
// pinned-invariant, a plain file-guard (it acts at Stop, on the settled file;
// pinned-spec-holds is the rule that ships a gate too), bound to a file carrying
// an `sr:invariant` marker whose fqn is a pinned spec reference `<repo>@<sha>:<path>#L<start>-<end>`. Two checks divide
// the work, cheap-gates-expensive:
//
//   - SCRIPT (pin-still-matches-head.sh): does the pin resolve (real commit, real
//     path, real line range) AND does the pinned range still match HEAD? Pure git
//     byte-comparison — catches spec drift with no model.
//   - JUDGE (code-upholds-invariant.md.j2): given the pinned text, does the marked
//     code actually enforce what it says?
//
// A file-guard acts only at Stop, so every refusal here arrives there and is read
// with BlockingErrorsFrom(proj, sess, "Stop"); res.Refused() (the PreToolUse deny
// marker) stays false for this rule.
//
// How each mechanism is driven:
//   - MARKER: written in the file's own content as a `// sr:invariant "<fqn>"`
//     line, so filemod.Scan lifts it onto the event's newMarkers and the guard's
//     `match: any(markers, .kind == "invariant")` selects the file.
//   - GIT: e.GitInit + real commits via e.Git; the spec file is committed at a
//     known sha, and the marker's fqn pins a line range at that sha. The stale
//     case commits a reworded spec so HEAD's line no longer matches the pin.
//   - JUDGE verdict: InstallJudgeClaude supplies the model's pass/fail.
//
// The example is installed VERBATIM — its scripts ship executable (mode 100755),
// so the rule's own logic runs as a user would get it (the script-refusal tests
// carry an exec-bit regression tripwire that names it precisely if that regresses).

import "testing"

// biProject stands up a project with the business-invariants example installed
// VERBATIM (its scripts ship executable), and returns the project dir.
func biProject(t *testing.T, e *env) string {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, "business-invariants")
	return proj
}

// commitSpec writes a spec file, commits it, and returns the resulting HEAD sha.
func commitSpec(t *testing.T, e *env, proj, path, body, msg string) string {
	t.Helper()
	e.WriteFile(proj, path, body)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", msg)
	return e.Git(proj, "rev-parse", "HEAD")
}

// specV1 is a spec whose line 2 is the invariant the code pins to.
const specV1 = "an invariants spec\nan order total must never be negative\n(end)\n"

// invariantCode is a code file carrying the sr:invariant marker (fqn built per
// test) and code that upholds the never-negative invariant.
func invariantCode(fqn, body string) string {
	return "// sr:invariant \"" + fqn + "\"\n" + body
}

// T046_01: HAPPY PATH — the pin resolves and still matches HEAD, and the judge
// rules the marked code upholds the invariant. The write is admitted.
func TestT046_01_PinMatchesAndJudgePassesAdmits(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	sha := commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the guard clause enforces never-negative"}`)

	fqn := proj + "@" + sha + ":SPEC.md#L2-2"
	code := invariantCode(fqn, "func charge(total int) { if total < 0 { panic(\"never negative\") } }\n")

	sess := "s-046-01"
	res := e.Run(proj, sess, "add the invariant-upholding charge()", Turns("done",
		Write("w1", "src/charge.go", code),
	).ThenCommit("write the files"))

	if res.Refused() {
		t.Fatalf("a matching pin + a passing judge was refused at pre-tool: %s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a matching pin + a passing judge blocked the turn at Stop:\n%s", joinBlocks(blocks))
	}
}

// T046_02: SCRIPT REFUSAL — a STALE pin. HEAD moved (the spec was reworded after
// the marker was written), so the pinned range no longer matches HEAD. The
// SCRIPT catches this before the judge is ever asked, and its drift reason
// reaches the agent.
//
// The judge is stubbed to PASS here on purpose: if the block still arrives, it
// proves the SCRIPT — not the judge — refused. This is the "a stale pin blocks
// via the script even before the judge" case.
func TestT046_02_StalePinBlocksViaScript(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	// Commit v1 and capture its sha, then rewrite line 2 and commit v2 so HEAD's
	// line 2 differs from the pinned v1 text.
	shaV1 := commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")
	commitSpec(t, e, proj, "SPEC.md",
		"an invariants spec\nan order total must never be negative OR ZERO\n(end)\n", "spec v2 reworded")

	// Judge would pass — so a block can only come from the pin script.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script should refuse first"}`)

	fqn := proj + "@" + shaV1 + ":SPEC.md#L2-2" // pinned to the OLD wording
	code := invariantCode(fqn, "func charge(total int) { if total < 0 { panic(\"never negative\") } }\n")

	sess := "s-046-02"
	e.Run(proj, sess, "add code pinned to a since-reworded spec", Turns("done",
		Write("w1", "src/charge.go", code),
	).ThenCommit("write the files"))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a stale pin (HEAD moved) was not refused by the script")
	}
	joined := joinBlocks(blocks)
	// Exec-bit regression tripwire: the example is installed verbatim, so if the
	// shipped pin-still-matches-head.sh ever lost its 100755 mode the engine would
	// refuse it as unrunnable ("not executable") instead of running the pin check.
	// Naming that here keeps this test from failing with a confusing "drift reason
	// not found" and instead reports the real cause.
	if containsAll(joined, "not executable") {
		t.Fatalf("REGRESSION: the shipped pin-still-matches-head.sh is not executable — the engine "+
			"refused it as unrunnable rather than running the pin check:\n%s", joined)
	}
	// The script's own drift reason — proof it executed the pin comparison.
	if !containsAll(joined, "pinned to text that has since changed at HEAD", "pinned-invariant") {
		t.Fatalf("the stale-pin (script) reason did not reach the agent:\n%s", joined)
	}
}

// T046_03: SCRIPT REFUSAL — the pin does not RESOLVE at all. The fqn names a sha
// this checkout does not have, so the link is dead before any HEAD comparison.
// The script refuses; the judge never runs.
func TestT046_03_UnresolvablePinBlocksViaScript(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	// A committed spec exists, but the marker pins a bogus sha.
	_ = commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "should not be reached"}`)

	fqn := proj + "@" + "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" + ":SPEC.md#L2-2"
	code := invariantCode(fqn, "func charge(total int) {}\n")

	sess := "s-046-03"
	e.Run(proj, sess, "add code with a dead pin", Turns("done",
		Write("w1", "src/charge.go", code),
	).ThenCommit("write the files"))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a pin naming a sha the checkout lacks was not refused by the script")
	}
	joined := joinBlocks(blocks)
	if !containsAll(joined, "names a commit or path this checkout does not have", "pinned-invariant") {
		t.Fatalf("the unresolvable-pin (script) reason did not reach the agent:\n%s", joined)
	}
}

// T046_04: JUDGE REFUSAL — the pin resolves AND still matches HEAD (the script
// passes), but the judge rules the marked code does NOT uphold the invariant.
// The block therefore comes from the JUDGE, and its reasoning reaches the agent.
//
// This is the other half of the divide from T046_02: a matching pin + a
// judge-fail blocks via the judge. The content really upholds nothing (an empty
// body), so prepare/judge have a genuinely different input than the happy case —
// not just the stub flipping.
func TestT046_04_MatchingPinJudgeFailBlocksViaJudge(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	sha := commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "charge() has no guard clause, so it does not enforce never-negative"}`)

	fqn := proj + "@" + sha + ":SPEC.md#L2-2"
	// Code that does NOT uphold the invariant (no guard).
	code := invariantCode(fqn, "func charge(total int) { /* charges anything, even negative */ }\n")

	sess := "s-046-04"
	e.Run(proj, sess, "add code that ignores the invariant", Turns("done",
		Write("w1", "src/charge.go", code),
	).ThenCommit("write the files"))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a matching pin with code that violates the invariant was not refused by the judge")
	}
	joined := joinBlocks(blocks)
	if !containsAll(joined, "no guard clause", "pinned-invariant") {
		t.Fatalf("the judge's reasoning did not reach the agent:\n%s", joined)
	}
}

// T046_05: THE MARKER ANCHORS THE RULE. A file with NO sr:invariant marker is
// not selected by the guard at all — even though the judge is stubbed to FAIL,
// nothing blocks, because the guard never fires. The control that proves the
// marker (not the path) is what binds the rule.
func TestT046_05_NoMarkerDoesNotFire(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	_ = commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")
	// A failing verdict that must never be reached, since the guard should not match.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "must not be reached — no marker on the file"}`)

	sess := "s-046-05"
	res := e.Run(proj, sess, "add plain unmarked code", Turns("done",
		Write("w1", "src/plain.go", "func plain() {}\n"),
	).ThenCommit("write the files"))

	if res.Refused() {
		t.Fatalf("an unmarked file was refused at pre-tool — the guard fired where it should not:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("an unmarked file blocked at Stop — the invariant guard matched a file with no sr:invariant marker:\n%s",
			joinBlocks(blocks))
	}
}

// T046_06: SAME MARKER, WITH vs WITHOUT — proves the marker is load-bearing by
// writing the SAME code body in two SEPARATE projects, once carrying the (stale)
// sr:invariant marker and once with it stripped. With the marker the stale pin
// is refused; strip the marker and the identical body is admitted. The only
// difference is the marker, so this isolates it as the thing that arms the rule.
//
// Two projects, not two cycles in one: a file-guard re-checks every outstanding
// not-fine file at Stop, so a leftover bad file from a WITH cycle would re-fire
// in a later WITHOUT cycle of the SAME project and the "no marker admits" half
// would read a refusal that is really about the leftover.
func TestT046_06_MarkerPresenceIsWhatDrivesTheGuard(t *testing.T) {
	body := "func charge(total int) { if total < 0 { panic(\"x\") } }\n"

	// WITH the marker (stale pin): the guard fires and the script refuses.
	{
		e := newEnv(t)
		proj := biProject(t, e)
		shaV1 := commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")
		commitSpec(t, e, proj, "SPEC.md",
			"an invariants spec\nan order total must never be negative OR ZERO\n(end)\n", "spec v2 reworded")
		e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
		fqn := proj + "@" + shaV1 + ":SPEC.md#L2-2" // stale pin
		sess := "s-046-06-with"
		e.Run(proj, sess, "code with the stale-pinned marker", Turns("done",
			Write("w1", "src/charge.go", invariantCode(fqn, body)),
		).ThenCommit("write the files"))
		if len(e.BlockingErrorsFrom(proj, sess, "Stop")) == 0 {
			t.Fatalf("WITH the marker, the stale pin was not refused")
		}
	}

	// WITHOUT the marker: the identical body is not the guard's business.
	{
		e := newEnv(t)
		proj := biProject(t, e)
		_ = commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")
		e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
		sess := "s-046-06-without"
		res := e.Run(proj, sess, "the same code with no marker", Turns("done",
			Write("w1", "src/charge.go", body),
		).ThenCommit("write the files"))
		if res.Refused() {
			t.Fatalf("WITHOUT the marker, the same code was refused at pre-tool:\n%s", res.Output)
		}
		if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
			t.Fatalf("WITHOUT the marker, the same code blocked — the guard is not marker-anchored:\n%s",
				joinBlocks(blocks))
		}
	}
}
