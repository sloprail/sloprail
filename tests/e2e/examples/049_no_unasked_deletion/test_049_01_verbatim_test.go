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
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readMemory reads a file of the project as text.
func readMemory(t *testing.T, proj, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(proj, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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

// T049_18: A CITED DELETE GOES TO THE JUDGE — the whole-file removal made the
// grounded way, `sr-file delete <path> --cite:user '<quote>'`, carries the user's
// words on the event; the requirement is met and the judge (stubbed pass) admits
// it. The file is gone. The complement of T049_05: `rm` cites nothing, this does.
func TestT049_18_CitedDeleteAdmits(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the user asked to delete this file"}`)

	seedCommittedMemory(t, e, proj, "memories/topic.md", "some content\nmore content\n")

	res := e.Run(proj, "s-049-18", "delete the memory file", Turns("done",
		Bash("d1", "sr-file delete memories/topic.md --cite:user 'delete the memory file'"),
	).ThenCommit("write the files", harness.CitesUser("delete the memory file")))

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
	).ThenCommit("write the files"))

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
// Separate projects, not two cycles in one: a pre-write deny leaves the original
// file on disk, and a leftover not-fine file from the WITHOUT branch would be
// re-checked in a shared project and confuse the WITH branch's admit.
func TestT049_07_MarkerAuthorizesTheRemoval(t *testing.T) {
	prompt := "please remove the second line"

	// WITHOUT the marker: refused for having no sr:asked marker.
	{
		e := newEnv(t)
		proj := nudProject(t, e)
		e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
		seedCommittedMemory(t, e, proj, "memories/topic.md", "keep this line\nremove the second line\n")
		sess := "s-049-07-without"
		res := e.Run(proj, sess, prompt, Turns("done",
			Write("w1", "memories/topic.md", "keep this line\n"),
		).ThenCommit("write the files"))
		if !res.Refused() || !res.Saw("must cite the user's own words (--cite:user)") {
			t.Fatalf("WITHOUT the marker, expected the no-marker refusal:\n%s", res.Output)
		}
	}

	// WITH a grounded marker: the identical removal is admitted (the quote resolves
	// to the user's own prompt, and the judge — stubbed pass — finds it clean).
	{
		e := newEnv(t)
		proj := nudProject(t, e)
		e.InstallJudgeClaude(`{"pass": true, "reasoning": "only the asked line removed"}`)
		seedCommittedMemory(t, e, proj, "memories/topic.md", "keep this line\nremove the second line\n")
		sess := "s-049-07-with"
		res := e.Run(proj, sess, prompt, Turns("done",
			srWrite("w1", "memories/topic.md", "keep this line\n", "please remove the second line"),
		).ThenCommit("write the files", harness.CitesUser("please remove the second line")))
		if res.Refused() {
			t.Fatalf("WITH a grounded marker, the identical removal was refused — the marker did not authorize it:\n%s", res.Output)
		}
	}
}

// T049_08: GROUNDED ASK ADMITS — the headline happy path for a REMOVAL. A removal
// citing the user's own words resolves against the transcript, meets the
// requirement, and the judge rules the change clean & absolute. The write is
// admitted. This runs against the SHIPPED example verbatim.
func TestT049_08_GroundedAskAdmits(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "only the asked line was removed; content stated absolutely"}`)

	seedCommittedMemory(t, e, proj, "memories/topic.md", "keep this line\nremove the second line\n")

	prompt := "please remove the second line"
	sess := "s-049-08"
	res := e.Run(proj, sess, prompt, Turns("done",
		srWrite("w1", "memories/topic.md", "keep this line\n", "please remove the second line"),
	).ThenCommit("write the files", harness.CitesUser("please remove the second line")))

	if res.Refused() {
		t.Fatalf("a grounded-ask removal was refused by the shipped rule:\n%s", res.Output)
	}
}

// T049_09: GROUNDED ASK, UNCLEAN CHANGE — the collateral case (coverage bar f).
// The removal has a real grounded ask, so it reaches the judge, but the
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
	seedCommittedMemory(t, e, proj, "memories/topic.md",
		"keep this line\nremove the second line\nprovenance: derived from source X\n")

	prompt := "please remove the second line"
	sess := "s-049-09"
	res := e.Run(proj, sess, prompt, Turns("done",
		// Removes the asked line AND, collaterally, the provenance line.
		srWrite("w1", "memories/topic.md", "keep this line\n", "please remove the second line"),
	).ThenCommit("write the files", harness.CitesUser("please remove the second line")))

	// The gate holds no judge: the cited removal lands. The judge (in the
	// file-guard) blocks the turn at Stop.
	if res.Refused() {
		t.Fatalf("the gate (citation only, no model) refused a cited removal:\n%s", res.Output)
	}
	blocks := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(blocks, "SR049J the diff also removed a provenance line") {
		t.Fatalf("the judge's clean/absolute reasoning did not block the turn at Stop:\n%s", blocks)
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

	seedCommittedMemory(t, e, proj, "memories/topic.md", "keep this line\nremove the second line\n")

	// The user asked to remove the second line; the marker quotes something else
	// entirely, which resolves to nothing in the trajectory.
	prompt := "please remove the second line"
	sess := "s-049-10"
	res := e.Run(proj, sess, prompt, Turns("done",
		srWrite("w1", "memories/topic.md", "keep this line\n", "delete absolutely everything in the project"),
	).ThenCommit("write the files", harness.CitesUser("delete absolutely everything in the project")))

	if !res.Refused() {
		t.Fatalf("a fabricated ask (quote the user never said) was NOT refused:\n%s", res.Output)
	}
	// sr-file's dry run fails on the unresolvable quote, and the refusal quotes
	// its own reason rather than a generic "could not compute".
	if !res.Saw("sr-file said") || !res.Saw("does not resolve") {
		t.Fatalf("the fabricated-ask reason did not reach the agent:\n%s", res.Output)
	}
}

// T049_20: a `sed -i` of a memory is a write whose result the engine cannot work
// out ahead, so the gate cannot show it preserves content and refuses it before
// it runs — the file is untouched. Nothing here can carry a citation.
func TestT049_20_SedInPlaceIsRefusedByTheGate(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the gate refuses first"}`)

	seedCommittedMemory(t, e, proj, "memories/topic.md", "keep this line\nremove this line\n")

	res := e.Run(proj, "s-049-20", "tidy the memory", Turns("done",
		Bash("b1", "sed -i.bak '/remove this line/d' memories/topic.md"),
	).ThenCommit("write the files"))
	if !res.Refused() || !res.Saw(`gate \"preserves-unasked-content\"`) {
		t.Fatalf("a sed -i of a memory was not refused by the gate:\n%s", res.Output)
	}
	if got := readMemory(t, proj, "memories/topic.md"); got != "keep this line\nremove this line\n" {
		t.Errorf("the refused sed -i reached the memory:\n%s", got)
	}
}

// T049_21: one `rm` of two memories is asked about EVERY file: both are named in
// the one refusal, and neither is deleted.
func TestT049_21_RmOfTwoMemoriesNamesBoth(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the requirement refuses first"}`)

	seedCommittedMemory(t, e, proj, "memories/a.md", "fact a\n")
	seedCommittedMemory(t, e, proj, "memories/b.md", "fact b\n")

	res := e.Run(proj, "s-049-21", "clean up", Turns("done",
		Bash("d1", "rm memories/a.md memories/b.md"),
	).ThenCommit("write the files"))
	if !res.Refused() || !res.Saw("memories/a.md") || !res.Saw("memories/b.md") {
		t.Fatalf("an rm of two memories was not refused naming both:\n%s", res.Output)
	}
	if !e.Exists(proj, "memories/a.md") || !e.Exists(proj, "memories/b.md") {
		t.Errorf("a refused rm still deleted a memory")
	}
}

// T049_22: the Stop after-check. A script rewriting a memory is not a write the
// engine sees ahead, so the gate never asks; the file-guard of the same name asks
// at Stop, and the settled change removes a line with no citation, so it blocks.
func TestT049_22_ScriptRewriteIsCaughtAtStop(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "irrelevant — the requirement refuses first"}`)

	seedCommittedMemory(t, e, proj, "memories/topic.md", "keep this line\nremove this line\n")

	sess := "s-049-22"
	e.Run(proj, sess, "tidy the memory", Turns("done",
		Bash("b1", `python3 -c "open('memories/topic.md','w').write('keep this line\\n')"`),
	).ThenCommit("write the files"))
	if got := readMemory(t, proj, "memories/topic.md"); got != "keep this line\n" {
		t.Fatalf("the script rewrite did not land, so this no longer tests the Stop after-check:\n%s", got)
	}
	joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n---\n")
	if !strings.Contains(joined, "preserves-unasked-content") || !strings.Contains(joined, "must cite the user's own words") {
		t.Fatalf("an uncited script rewrite that dropped a line was not refused at Stop:\n%s", joined)
	}
}
