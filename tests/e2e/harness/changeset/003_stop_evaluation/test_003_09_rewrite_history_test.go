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

// T003_09: an amend that changes SHAs but not content is a judge CACHE HIT. The
// range holds the same one commit as before, so its fingerprint is the same and the
// stored verdict is replayed without asking the model.
// sr:proves cache/verdict-identity
func TestT003_09_AnAmendThatChangesShasButNotContentIsACacheHit(t *testing.T) {
	e, proj := startedJudgeProject(t, "s-003-09", verdictFail)

	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	before := e.CommitAll(proj, "add a")
	r := e.StopJudged(proj, "s-003-09", false)
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

	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	e.CommitAll(proj, "add a")
	if r := e.StopJudged(proj, "s-003-10", false); harness.Blocked(r) {
		t.Fatalf("the passing judge refused:\n%s", r.Output)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("premise: one judge call, got %d", n)
	}

	// The passed head is rewritten (reworded), and another commit lands on it.
	e.Git(proj, "commit", "-q", "--amend", "-m", "add a, reworded")
	e.WriteFile(proj, "docs/b.md", "the release is Monday\n")
	e.CommitAll(proj, "add b")
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictFail)

	r := e.StopJudged(proj, "s-003-10", false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "JUDGE-SAYS-NO") {
		t.Fatalf("the rewritten range was not judged and refused:\n%s", r.Output)
	}
	prompt := e.JudgePrompt(proj, promptFile)
	for _, want := range []string{"docs/a.md(A)", "docs/b.md(A)", "[add a, reworded]", "[add b]"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the range after the amend should be the whole one from the floor; the prompt lacks %q:\n%s", want, prompt)
		}
	}

	// Fixed (the new commit reverted, and a judge that now passes), the same range passes.
	e.Git(proj, "revert", "--no-edit", "HEAD")
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictPass)
	if r := e.StopJudged(proj, "s-003-10", false); harness.Blocked(r) {
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
	if r := e.StopJudged(proj, "s-003-30", false); harness.Blocked(r) {
		t.Fatalf("the passing judge refused:\n%s", r.Output)
	}

	e.Git(proj, "reset", "-q", "--soft", floor)
	e.Git(proj, "commit", "-q", "-m", "add a, squashed")
	e.WriteFile(proj, "docs/b.md", "the release is Monday\n")
	e.CommitAll(proj, "add b")
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictFail)

	r := e.StopJudged(proj, "s-003-30", false)
	if !harness.Blocked(r) || !strings.Contains(r.Output, "JUDGE-SAYS-NO") {
		t.Fatalf("the squashed range was not judged and refused:\n%s", r.Output)
	}
	prompt := e.JudgePrompt(proj, promptFile)
	for _, want := range []string{"docs/a.md(A)", "docs/b.md(A)", "[add a, squashed]", "[add b]"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the range after the soft reset should start at the merge base; the prompt lacks %q:\n%s", want, prompt)
		}
	}

	// Fixed (the new commit reverted, and a judge that now passes), the same range passes.
	e.Git(proj, "revert", "--no-edit", "HEAD")
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictPass)
	if r := e.StopJudged(proj, "s-003-30", false); harness.Blocked(r) {
		t.Fatalf("the fixed range was still refused:\n%s", r.Output)
	}
}

// T003_11: the same through a rebase. The passed head is rewritten onto new
// upstream work; the range widens to include it, but what the judge is about is the
// matched files' content, which the rebase did not change: the stored pass is a hit
// (no second judge call), and a further Stop judges nothing either.
func TestT003_11_ARebasedWatermarkFallsBackAndTheRangeIsJudgedAgain(t *testing.T) {
	e, proj := startedJudgeProject(t, "s-003-11", verdictPass)
	floor := e.Git(proj, "rev-parse", "HEAD")

	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	e.CommitAll(proj, "add a")
	if r := e.StopJudged(proj, "s-003-11", false); harness.Blocked(r) {
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

	if r := e.StopJudged(proj, "s-003-11", false); harness.Blocked(r) {
		t.Fatalf("a rebased range that passes was refused:\n%s", r.Output)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("the rebased range holds the same matched content, so the stored pass is a hit and the judge is not asked again (%d calls)", n)
	}

	// It passed at the new head: nothing new is judged.
	if r := e.StopJudged(proj, "s-003-11", false); harness.Blocked(r) {
		t.Fatalf("refused:\n%s", r.Output)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("a Stop after the pass asked the judge again (%d calls)", n)
	}
}
