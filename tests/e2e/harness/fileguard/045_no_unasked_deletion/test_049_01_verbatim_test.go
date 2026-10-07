package e2e

// TODO(D3): drive verdict via a10n-claude-mock once a10n-cli#470 lands + new mock
// on PATH; today InstallJudgeClaude supplies the verdict.
//
// Use case: no-unasked-deletion (unit 17). A GATE on `event.path startsWith
// "memories/" and event.path endsWith ".md"` (PreFileWrite and PreFileDelete,
// refusing BEFORE the write lands — the content that would be lost is still on
// disk at Pre time), with a plain file-guard of the same name as the Stop
// after-check. "Asked" is a CITATION of the user's
// words on the command that makes the change (`sr-file ... --cite:user '<quote>'`),
// never a marker in the file.
//
// One conditional REQUIREMENT + one JUDGE:
//   - REQUIRE a user citation `when: ./removes-content.sh`: pure additions need
//     none; a removal (or a delete) citing nothing the user said is refused by the
//     engine; an unknown result (a quote that does not resolve, so sr-file's dry
//     run fails, or sr-file mixed into a longer line) fails closed.
//   - JUDGE (change-is-clean-and-absolute.md.j2), reached for a cited removal only
//     (its prepare skips a pure addition): the change is clean & targeted (only
//     what the cited words asked) and absolute (states the final content, not a
//     delta narrative).
//
// Because the rule is a GATE, refusals arrive at PRE-TOOL as a deny — read
// with res.Refused() and res.Saw(reason), NOT at Stop. A cited ADMIT lets the
// change land.
//
// How each mechanism is driven:
//   - CITATION: an `sr-file write|delete ... --cite:user '<quote>'` Bash turn run on
//     its own, so the engine dry-runs it for the exact result and resolves the
//     quote against the transcript (the user's prompt, seeded by the harness).
//   - UNCITED: a plain Write turn, or an `rm` Bash turn — neither can carry one.
//   - JUDGE verdict: InstallJudgeClaude supplies the model's pass/fail.

import (
	"testing"
)

// seedCommittedMemory writes a memory file and commits it, so it is in the git
// baseline the Stop-time diff is computed against.
func seedCommittedMemory(t *testing.T, e *env, proj, rel, body string) {
	t.Helper()
	e.WriteFile(proj, rel, body)
	e.CommitSeedThenRules(proj, "seed "+rel)
}

// nudProject stands up a project with the no-unasked-deletion example installed
// verbatim (its scripts ship executable, so no chmod is needed).
func nudProject(t *testing.T, e *env) string {
	t.Helper()
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, "no-unasked-deletion")
	return proj
}

// T049_01: HAPPY PATH (pure additions) — an append removes nothing, so it needs
// no citation (removes-content.sh waives it) and the judge is skipped: the write
// is admitted. TestT049_03 flips the verdict and still gets an admit.
func TestT049_01_PureAdditionAdmits(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "append only, nothing removed"}`)

	seedCommittedMemory(t, e, proj, "memories/topic.md", "first line\n")

	sess := "s-049-01"
	res := e.Run(proj, sess, "append a line to the memory", Turns("done",
		Write("w1", "memories/topic.md", "first line\nsecond line appended\n"),
	).ThenCommit("write the files"))

	if res.Refused() {
		t.Fatalf("a pure-addition write was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/topic.md") {
		t.Fatalf("the memory file is gone after an admitted append")
	}
}

// T049_02: REQUIREMENT REFUSAL — a REMOVAL citing nothing. Lines were removed and
// the change carries no citation, so the engine refuses at pre-tool
// (the unasked rewrite this rule exists to catch), and its reason reaches the
// agent. The old content is still on disk (the write was denied, not undone).
func TestT049_02_RemovalWithoutMarkerBlocks(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the requirement refuses first"}`)

	seedCommittedMemory(t, e, proj, "memories/topic.md", "keep this line\nremove this line\n")

	sess := "s-049-02"
	res := e.Run(proj, sess, "silently drop a line", Turns("done",
		Write("w1", "memories/topic.md", "keep this line\n"), // dropped a line, NO marker
	).ThenCommit("write the files"))

	if !res.Refused() {
		t.Fatalf("a removal with no sr:asked marker was NOT refused at pre-tool:\n%s", res.Output)
	}
	if !res.Saw("must cite the user's own words (--cite:user)") {
		t.Fatalf("the uncited-removal reason did not reach the agent:\n%s", res.Output)
	}
	// The rule's hint advises (append, or cite the ask) but names no command:
	// the refusal must still carry one the agent can run.
	if !res.Saw("sr-file edit memories/topic.md") {
		t.Errorf("the refusal carries no runnable sr-file command:\n%s", res.Output)
	}
	// The write was denied, so the file still holds its original content.
	if !e.Exists(proj, "memories/topic.md") {
		t.Fatalf("a pre-write deny should leave the original file on disk")
	}
}

// T049_03: a PURE ADDITION never reaches the judge — its prepare skips it, so
// even a failing verdict stub cannot block the append, and the write lands. An
// append removes nothing, so nothing needed authorizing and no model call is due.
func TestT049_03_PureAdditionSkipsTheJudge(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR049 the judge ran on a pure addition"}`)

	seedCommittedMemory(t, e, proj, "memories/topic.md", "first line\n")

	sess := "s-049-03"
	res := e.Run(proj, sess, "append a line to the memory", Turns("done",
		Write("w1", "memories/topic.md", "first line\nsecond line appended\n"),
	).ThenCommit("write the files"))

	if res.Refused() || res.Saw("SR049 the judge ran on a pure addition") {
		t.Fatalf("a pure addition was judged:\n%s", res.Output)
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

	seedCommittedMemory(t, e, proj, "memories/topic.md", "some content\nmore content\n")

	sess := "s-049-05"
	res := e.Run(proj, sess, "delete the memory file", Turns("done",
		Bash("d1", "rm memories/topic.md"),
	).ThenCommit("write the files"))

	if !res.Refused() {
		t.Fatalf("an rm of a memories file was NOT refused at pre-tool:\n%s", res.Output)
	}
	if !res.Saw("must cite the user's own words (--cite:user)") || !res.Saw("sr-file delete memories/topic.md") {
		t.Fatalf("the uncited-delete reason did not reach the agent:\n%s", res.Output)
	}
	// Refused at pre-tool means the delete was denied — the file survives.
	if !e.Exists(proj, "memories/topic.md") {
		t.Fatalf("a deny should have prevented the rm — the file is gone")
	}
}
