package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// History rewritten under a passed rule, driven through real Stops. The watermark
// is the head a rule last passed at; an amend or a rebase makes that SHA
// unreachable (a10n never checked, and its diffs silently shrank to the latest
// attempt). The dropped base falls back to the floor, and what is judged — and
// what is remembered — depends on the CONTENT of the range, never on SHAs.

// startedJudgeProject is a judged-rule project with a session that has begun and
// been through one clean Stop.
func startedJudgeProject(t *testing.T, sess, verdict string) (*Env, string) {
	t.Helper()
	e, proj := judgeProject(t, verdict)
	e.Run(proj, sess, "hello", Turns("done", Bash("b1", "true")))
	return e, proj
}

func droppedWatermark(t *testing.T, e *Env, proj, sess string) string {
	t.Helper()
	res := e.ChecksSQL(proj, sess, "select json_extract(metadata, '$.droppedWatermark') as dropped, json_extract(metadata, '$.baseOrigin') as origin from check_runs where check_id = 'file-guard/docs' order by run_at desc, rowid desc limit 1")
	return res.Output
}

// T003_09: an amend that changes SHAs but not content is a judge CACHE HIT. The
// watermark's head is gone, so the range falls back to the floor — and holds the
// same one commit as before, so its fingerprint is the same and the verdict is
// replayed without asking the model.
func TestT003_09_AnAmendThatChangesShasButNotContentIsACacheHit(t *testing.T) {
	e, proj := startedJudgeProject(t, "s-003-09", verdictFail)

	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	before := e.CommitAll(proj, "add a")
	r := e.StopNow(proj, "s-003-09", false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "JUDGE-SAYS-NO") {
		t.Fatalf("the failing judge did not refuse:\n%s", r.Output)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("premise: one judge call, got %d", n)
	}

	// Same message, same tree — a different commit.
	e.Git(proj, "commit", "-q", "--amend", "--no-edit", "--date", "2001-01-01T00:00:00")
	after := e.Git(proj, "rev-parse", "HEAD")
	if after == before {
		t.Fatal("premise: the amend did not change the commit's SHA")
	}

	r = e.StopNow(proj, "s-003-09", false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "JUDGE-SAYS-NO") {
		t.Fatalf("the replayed failure was not refused after the amend:\n%s", r.Output)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("a rewrite that changed SHAs but not content asked the judge again (%d calls): the fingerprint must never depend on a SHA", n)
	}
}

// T003_10: an amend orphans the passed head. The dropped watermark is reported, the
// range falls back to the floor and holds BOTH the reworded commit and the new one,
// and the judge — now failing — refuses that whole range. Nothing is advanced by a
// refusal: the range stays the same one until it passes.
func TestT003_10_AnAmendedAwayWatermarkFallsBackToTheFloorAndIsJudgedAgain(t *testing.T) {
	e, proj := startedJudgeProject(t, "s-003-10", verdictPass)
	floor := e.Git(proj, "rev-parse", "HEAD")

	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	e.CommitAll(proj, "add a")
	if r := e.StopNow(proj, "s-003-10", false); harness.Blocked(r) {
		t.Fatalf("the passing judge refused:\n%s", r.Output)
	}
	passedHead := e.Git(proj, "rev-parse", "HEAD")
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("premise: one judge call, got %d", n)
	}

	// The passed head is rewritten (reworded), and another commit lands on it.
	e.Git(proj, "commit", "-q", "--amend", "-m", "add a, reworded")
	e.WriteFile(proj, "docs/b.md", "the release is Monday\n")
	e.CommitAll(proj, "add b")
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictFail)

	r := e.StopNow(proj, "s-003-10", false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "JUDGE-SAYS-NO") {
		t.Fatalf("the rewritten range was not judged and refused:\n%s", r.Output)
	}
	prompt := e.JudgePrompt(proj, promptFile)
	for _, want := range []string{"docs/a.md(A)", "docs/b.md(A)", "[add a, reworded]", "[add b]", "BASE=" + floor} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the range after the amend should be the whole one from the floor; the prompt lacks %q:\n%s", want, prompt)
		}
	}
	if got := droppedWatermark(t, e, proj, "s-003-10"); !strings.Contains(got, passedHead) {
		t.Fatalf("the run should record the watermark it had to drop (%s):\n%s", passedHead, got)
	}

	// Fixed (the new commit reverted, and a judge that now passes), the same range passes.
	e.Git(proj, "revert", "--no-edit", "HEAD")
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictPass)
	if r := e.StopNow(proj, "s-003-10", false); harness.Blocked(r) {
		t.Fatalf("the fixed range was still refused:\n%s", r.Output)
	}
}

// T003_30: the same through a soft reset that recommits (the usual "squash my commits"):
// the passed head is orphaned, its merge base with HEAD is the floor, and the squashed
// commit and the new one are judged again.
func TestT003_30_ASoftResetWatermarkFallsBackToItsMergeBaseAndIsJudgedAgain(t *testing.T) {
	e, proj := startedJudgeProject(t, "s-003-30", verdictPass)
	floor := e.Git(proj, "rev-parse", "HEAD")

	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	e.CommitAll(proj, "add a")
	if r := e.StopNow(proj, "s-003-30", false); harness.Blocked(r) {
		t.Fatalf("the passing judge refused:\n%s", r.Output)
	}
	passedHead := e.Git(proj, "rev-parse", "HEAD")

	e.Git(proj, "reset", "-q", "--soft", floor)
	e.Git(proj, "commit", "-q", "-m", "add a, squashed")
	e.WriteFile(proj, "docs/b.md", "the release is Monday\n")
	e.CommitAll(proj, "add b")
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictFail)

	r := e.StopNow(proj, "s-003-30", false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "JUDGE-SAYS-NO") {
		t.Fatalf("the squashed range was not judged and refused:\n%s", r.Output)
	}
	prompt := e.JudgePrompt(proj, promptFile)
	for _, want := range []string{"docs/a.md(A)", "docs/b.md(A)", "[add a, squashed]", "[add b]", "BASE=" + floor} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the range after the soft reset should start at the merge base; the prompt lacks %q:\n%s", want, prompt)
		}
	}
	if got := droppedWatermark(t, e, proj, "s-003-30"); !strings.Contains(got, passedHead) {
		t.Fatalf("the run should record the watermark it had to drop (%s):\n%s", passedHead, got)
	}

	// Fixed (the new commit reverted, and a judge that now passes), the same range passes.
	e.Git(proj, "revert", "--no-edit", "HEAD")
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictPass)
	if r := e.StopNow(proj, "s-003-30", false); harness.Blocked(r) {
		t.Fatalf("the fixed range was still refused:\n%s", r.Output)
	}
}

// T003_11: the same through a rebase. The passed head is rewritten onto new
// upstream work; the range widens to include it (named in `others` and in the
// commits) and is judged again — passing here — and the watermark then moves to the
// new head, so a further Stop judges nothing.
func TestT003_11_ARebasedWatermarkFallsBackAndTheRangeIsJudgedAgain(t *testing.T) {
	e, proj := startedJudgeProject(t, "s-003-11", verdictPass)
	floor := e.Git(proj, "rev-parse", "HEAD")

	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	e.CommitAll(proj, "add a")
	if r := e.StopNow(proj, "s-003-11", false); harness.Blocked(r) {
		t.Fatalf("the passing judge refused:\n%s", r.Output)
	}
	passedHead := e.Git(proj, "rev-parse", "HEAD")

	// Upstream work lands under it, and the branch is rebased onto that.
	e.Git(proj, "checkout", "-q", "-b", "upstream", floor)
	e.WriteFile(proj, "upstream.txt", "someone else's work\n")
	e.CommitAll(proj, "upstream work")
	e.Git(proj, "checkout", "-q", "main")
	e.Git(proj, "rebase", "-q", "upstream")
	if e.Git(proj, "rev-parse", "HEAD") == passedHead {
		t.Fatal("premise: the rebase did not rewrite the passed head")
	}

	if r := e.StopNow(proj, "s-003-11", false); harness.Blocked(r) {
		t.Fatalf("a rebased range that passes was refused:\n%s", r.Output)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 2 {
		t.Fatalf("the widened range should have been judged again (%d calls)", n)
	}
	if !strings.Contains(e.JudgePrompt(proj, promptFile), "[upstream work]") {
		t.Fatalf("the rebased range should carry the upstream commit:\n%s", e.JudgePrompt(proj, promptFile))
	}
	if got := droppedWatermark(t, e, proj, "s-003-11"); !strings.Contains(got, passedHead) {
		t.Fatalf("the dropped watermark %s was not recorded:\n%s", passedHead, got)
	}

	// It passed at the new head: nothing new is judged.
	if r := e.StopNow(proj, "s-003-11", false); harness.Blocked(r) {
		t.Fatalf("refused:\n%s", r.Output)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 2 {
		t.Fatalf("a Stop after the pass asked the judge again (%d calls)", n)
	}
}
