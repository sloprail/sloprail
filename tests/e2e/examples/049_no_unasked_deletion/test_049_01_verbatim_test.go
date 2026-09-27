package e2e

// TODO(D3): drive verdict via a10n-claude-mock once a10n-cli#470 lands + new mock
// on PATH; today InstallJudgeClaude supplies the verdict.
//
// Use case: no-unasked-deletion (unit 17). A file-guard bound to
// `path startsWith "memories/" and path endsWith ".md"`, PREVENTIVE (fires at
// PreFileUpdate/PreFileDelete, refusing BEFORE the write lands — the content that
// would be lost is still on disk at Pre time). "Asked" is a CITATION of the user's
// words on the command that makes the change (`sr-file ... --cite:user '<quote>'`),
// never a marker in the file.
//
// One SCRIPT + one JUDGE:
//   - SCRIPT (removal-has-a-grounded-ask.sh): pure additions pass; a removal (or a
//     delete) citing nothing the user said is refused; one citing the user's words
//     passes to the judge; an unknown result (a quote that does not resolve, so
//     sr-file's dry run fails, or sr-file mixed into a longer line) fails closed.
//   - JUDGE (change-is-clean-and-absolute.md.j2): the change is clean & targeted
//     (only what the cited words asked) and absolute (states the final content, not
//     a delta narrative).
//
// Because the guard is PREVENTIVE, refusals arrive at PRE-TOOL as a deny — read
// with res.Refused() and res.Saw(reason), NOT at Stop. A cited ADMIT lets the
// change land.
//
// How each mechanism is driven:
//   - CITATION: an `sr-file write|delete ... --cite:user '<quote>'` Bash turn run on
//     its own, so the engine dry-runs it for the exact result and resolves the
//     quote against the transcript (the user's prompt, seeded by the harness).
//   - UNCITED: a plain Write turn, or an `rm` Bash turn — neither can carry one.
//   - JUDGE verdict: InstallJudgeClaude supplies the model's pass/fail.

import "testing"

// nudProject stands up a project with the no-unasked-deletion example installed
// verbatim (its scripts ship executable, so no chmod is needed).
func nudProject(t *testing.T, e *env) string {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, "no-unasked-deletion")
	return proj
}

// T049_01: HAPPY PATH (pure additions) — an append that removes nothing passes
// the script (no removed lines) and the judge (nothing to be unclean about), so
// the write is admitted.
//
// The judge DOES run here (it is the second check, and a passing script lets it
// run even on additions), so a passing verdict is what admits — proven by
// TestT049_03 which flips only the verdict and gets a block.
func TestT049_01_PureAdditionAdmits(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "append only, nothing removed"}`)

	e.WriteFile(proj, "memories/topic.md", "first line\n")

	sess := "s-049-01"
	res := e.Run(proj, sess, "append a line to the memory", Turns("done",
		Write("w1", "memories/topic.md", "first line\nsecond line appended\n"),
	))

	if res.Refused() {
		t.Fatalf("a pure-addition write was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/topic.md") {
		t.Fatalf("the memory file is gone after an admitted append")
	}
}

// T049_02: SCRIPT REFUSAL — a REMOVAL with NO sr:asked marker. Lines were removed
// and the file declares no authorizing quote, so the script refuses at pre-tool
// (the unasked rewrite this rule exists to catch), and its reason reaches the
// agent. The old content is still on disk (the write was denied, not undone).
func TestT049_02_RemovalWithoutMarkerBlocks(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script refuses first"}`)

	e.WriteFile(proj, "memories/topic.md", "keep this line\nremove this line\n")

	sess := "s-049-02"
	res := e.Run(proj, sess, "silently drop a line", Turns("done",
		Write("w1", "memories/topic.md", "keep this line\n"), // dropped a line, NO marker
	))

	if !res.Refused() {
		t.Fatalf("a removal with no sr:asked marker was NOT refused at pre-tool:\n%s", res.Output)
	}
	if !res.Saw("cites nothing the user said") {
		t.Fatalf("the no-marker (script) reason did not reach the agent:\n%s", res.Output)
	}
	// The write was denied, so the file still holds its original content.
	if !e.Exists(proj, "memories/topic.md") {
		t.Fatalf("a preventive deny should leave the original file on disk")
	}
}

// T049_03: JUDGE REFUSAL — a pure addition, but the judge returns a FAILING
// verdict. This is the same setup as T049_01 with only the verdict flipped, which
// proves the judge really runs on an addition (it is not short-circuited) and its
// reasoning reaches the agent. The block arrives at pre-tool (preventive).
func TestT049_03_JudgeFailBlocks(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR049 the added line narrates a delta instead of stating final content"}`)

	e.WriteFile(proj, "memories/topic.md", "first line\n")

	sess := "s-049-03"
	res := e.Run(proj, sess, "append a line to the memory", Turns("done",
		Write("w1", "memories/topic.md", "first line\nsecond line appended\n"),
	))

	if !res.Refused() {
		t.Fatalf("a failing judge verdict did not block the write at pre-tool:\n%s", res.Output)
	}
	if !res.Saw("SR049 the added line narrates a delta") {
		t.Fatalf("the judge's reasoning did not reach the agent:\n%s", res.Output)
	}
}

// T049_05: DELETE via `rm` — a delete of a memories/*.md file produces a
// PreFileDelete whose newContent is ABSENT, so the guard cannot show the write
// preserves content and fails CLOSED (refusing the delete at pre-tool). There is
// no Delete turn builder, so the delete is driven by an `rm` Bash turn; the file
// must exist first (a delete of a non-existent file produces no event).
func TestT049_05_RmDeleteFailsClosed(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — a delete has no computable result"}`)

	e.WriteFile(proj, "memories/topic.md", "some content\nmore content\n")

	sess := "s-049-05"
	res := e.Run(proj, sess, "delete the memory file", Turns("done",
		Bash("d1", "rm memories/topic.md"),
	))

	if !res.Refused() {
		t.Fatalf("an rm of a memories file was NOT refused at pre-tool:\n%s", res.Output)
	}
	if !res.Saw("cites nothing the user said asking for it") {
		t.Fatalf("the uncited-delete reason did not reach the agent:\n%s", res.Output)
	}
	// Refused at pre-tool means the delete was denied — the file survives.
	if !e.Exists(proj, "memories/topic.md") {
		t.Fatalf("a deny should have prevented the rm — the file is gone")
	}
}

// T049_18: A CITED DELETE GOES TO THE JUDGE — the whole-file removal made the
// grounded way, `sr-file delete <path> --cite:user '<quote>'`, carries the user's
// words on the event; the script passes it and the judge (stubbed pass) admits
// it. The file is gone. The complement of T049_05: `rm` cites nothing, this does.
func TestT049_18_CitedDeleteAdmits(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the user asked to delete this file"}`)

	e.WriteFile(proj, "memories/topic.md", "some content\nmore content\n")

	res := e.Run(proj, "s-049-18", "delete the memory file", Turns("done",
		Bash("d1", "sr-file delete memories/topic.md --cite:user 'delete the memory file'"),
	))

	if res.Refused() {
		t.Fatalf("a cited sr-file delete was refused:\n%s", res.Output)
	}
	if e.Exists(proj, "memories/topic.md") {
		t.Fatalf("the cited delete was admitted but the file is still there")
	}
}

// T049_06: DOES NOT FIRE OUTSIDE ITS MATCH — the guard binds only files under
// memories/ ending in .md. A removal from a file OUTSIDE that scope (a top-level
// notes.md, or a src/ file) is not the guard's business, so a wholesale rewrite
// that drops lines is admitted. Proves the match is scoped, not global.
func TestT049_06_NonMemoriesFileDoesNotFire(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	// A verdict that must never be reached, since the guard should not match.
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "must not be reached — outside memories/"}`)

	e.WriteFile(proj, "notes.md", "keep\ndrop this line\n") // top-level, not under memories/

	sess := "s-049-06"
	res := e.Run(proj, sess, "rewrite a non-memories file", Turns("done",
		Write("w1", "notes.md", "keep\n"), // dropped a line, but outside the guard's scope
	))

	if res.Refused() {
		t.Fatalf("a rewrite of a file outside memories/ was refused — the guard overreached:\n%s", res.Output)
	}
}

// T049_07: THE MARKER IS LOAD-BEARING — the SAME removal, WITH a grounded marker
// vs WITHOUT it, in separate projects. WITHOUT a marker the removal is refused for
// having no authorizing quote; WITH a marker whose quote is the user's own prompt
// (so cite resolves it) the identical removal is ADMITTED. The only difference is
// the marker, so this isolates the marker as what authorizes the removal — it is
// read and acted on, not ignored.
//
// Separate projects, not two cycles in one: a preventive deny leaves the original
// file on disk, and a leftover not-fine file from the WITHOUT branch would be
// re-checked in a shared project and confuse the WITH branch's admit.
func TestT049_07_MarkerAuthorizesTheRemoval(t *testing.T) {
	prompt := "please remove the second line"

	// WITHOUT the marker: refused for having no sr:asked marker.
	{
		e := newEnv(t)
		proj := nudProject(t, e)
		e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
		e.WriteFile(proj, "memories/topic.md", "keep this line\nremove the second line\n")
		sess := "s-049-07-without"
		res := e.Run(proj, sess, prompt, Turns("done",
			Write("w1", "memories/topic.md", "keep this line\n"),
		))
		if !res.Refused() || !res.Saw("cites nothing the user said") {
			t.Fatalf("WITHOUT the marker, expected the no-marker refusal:\n%s", res.Output)
		}
	}

	// WITH a grounded marker: the identical removal is admitted (the quote resolves
	// to the user's own prompt, and the judge — stubbed pass — finds it clean).
	{
		e := newEnv(t)
		proj := nudProject(t, e)
		e.InstallJudgeClaude(`{"pass": true, "reasoning": "only the asked line removed"}`)
		e.WriteFile(proj, "memories/topic.md", "keep this line\nremove the second line\n")
		sess := "s-049-07-with"
		res := e.Run(proj, sess, prompt, Turns("done",
			srWrite("w1", "memories/topic.md", "keep this line\n", "please remove the second line"),
		))
		if res.Refused() {
			t.Fatalf("WITH a grounded marker, the identical removal was refused — the marker did not authorize it:\n%s", res.Output)
		}
	}
}

// T049_08: GROUNDED ASK ADMITS — the headline happy path for a REMOVAL. A removal
// whose sr:asked quote IS the user's own words resolves via
// `cite --path "$transcript_path"`, passes the script, and the judge rules the
// change clean & absolute. The write is admitted. This runs against the SHIPPED
// example verbatim — the grounding is done by the shipped script itself.
func TestT049_08_GroundedAskAdmits(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "only the asked line was removed; content stated absolutely"}`)

	e.WriteFile(proj, "memories/topic.md", "keep this line\nremove the second line\n")

	prompt := "please remove the second line"
	sess := "s-049-08"
	res := e.Run(proj, sess, prompt, Turns("done",
		srWrite("w1", "memories/topic.md", "keep this line\n", "please remove the second line"),
	))

	if res.Refused() {
		t.Fatalf("a grounded-ask removal was refused by the shipped rule:\n%s", res.Output)
	}
}

// T049_09: GROUNDED ASK, UNCLEAN CHANGE — the collateral case (coverage bar f).
// The removal has a real grounded ask, so the script passes to the judge, but the
// diff ALSO dropped an unrelated provenance line the quote did not authorize. The
// JUDGE refuses (not clean & targeted), and its reasoning reaches the agent.
//
// The judge sees a REAL removal diff via the shipped prepare — a genuinely
// different input from the addition cases — so this exercises the judge's actual
// purpose (clean & absolute over a real deletion), not just the stub flipping. It
// runs against the shipped example verbatim.
func TestT049_09_GroundedAskUncleanChangeBlocksViaJudge(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR049J the diff also removed a provenance line the ask did not cover"}`)

	// The file has a provenance line the ask says nothing about.
	e.WriteFile(proj, "memories/topic.md",
		"keep this line\nremove the second line\nprovenance: derived from source X\n")

	prompt := "please remove the second line"
	sess := "s-049-09"
	res := e.Run(proj, sess, prompt, Turns("done",
		// Removes the asked line AND, collaterally, the provenance line.
		srWrite("w1", "memories/topic.md", "keep this line\n", "please remove the second line"),
	))

	if !res.Refused() {
		t.Fatalf("an unclean grounded-ask removal (collateral deletion) was NOT refused by the judge:\n%s", res.Output)
	}
	if !res.Saw("SR049J the diff also removed a provenance line") {
		t.Fatalf("the judge's clean/absolute reasoning did not reach the agent:\n%s", res.Output)
	}
}

// T049_10: FABRICATED ASK — a removal whose sr:asked quote resolves to NOTHING the
// user said (a quote the agent invented). The shipped script grounds the quote via
// cite, which finds no match and exits 1, so the SCRIPT refuses. This is the
// grounding working as intended, and the complement of T049_08: a genuine quote
// admits, a fabricated one is refused — proving cite is really consulted, not
// bypassed. It runs against the shipped example verbatim.
func TestT049_10_FabricatedAskBlocksViaScript(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the script refuses a fabricated ask"}`)

	e.WriteFile(proj, "memories/topic.md", "keep this line\nremove the second line\n")

	// The user asked to remove the second line; the marker quotes something else
	// entirely, which resolves to nothing in the trajectory.
	prompt := "please remove the second line"
	sess := "s-049-10"
	res := e.Run(proj, sess, prompt, Turns("done",
		srWrite("w1", "memories/topic.md", "keep this line\n", "delete absolutely everything in the project"),
	))

	if !res.Refused() {
		t.Fatalf("a fabricated ask (quote the user never said) was NOT refused:\n%s", res.Output)
	}
	// sr-file's dry run fails on the unresolvable quote, and the refusal quotes
	// its own reason rather than a generic "could not compute".
	if !res.Saw("sr-file could not compute the change") || !res.Saw("does not resolve") {
		t.Fatalf("the fabricated-ask reason did not reach the agent:\n%s", res.Output)
	}
}
