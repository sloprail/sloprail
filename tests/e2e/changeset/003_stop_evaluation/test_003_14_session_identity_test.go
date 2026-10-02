package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Verdicts are keyed by CONTENT (rule, rule hash, step, subject, fingerprint of
// the rendered judge prompt), not by conversation: any session judging the same
// content reuses a stored judge verdict, and different content is judged afresh.
// These use a model judge so a count of its prompts shows what was asked.

// T003_14: an unrelated conversation judging IDENTICAL content reuses the cached
// judge pass (the judge is asked once in total); judging DIFFERENT content asks
// the judge again.
func TestT003_14_AnUnrelatedSessionInheritsNoVerdicts(t *testing.T) {
	e, proj := judgeProject(t, verdictPass)
	e.Run(proj, "s-003-14-second", "look around", Turns("done", Bash("b0", "true")))

	e.Run(proj, "s-003-14-first", "write the doc", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
	))
	if n := stopBlocks(e, proj, "s-003-14-first"); n != 0 {
		t.Fatalf("premise: the clean commit was refused (%d blocks)", n)
	}
	asked := e.JudgeCalls(proj, promptFile, "")
	if asked != 1 {
		t.Fatalf("premise: the judge was asked %d times for the first session; want 1", asked)
	}

	// The unrelated session's range holds the very same content: the cached pass is reused.
	e.Run(proj, "s-003-14-second", "now look again", Turns("done", Bash("b1", "true")))
	if n := stopBlocks(e, proj, "s-003-14-second"); n != 0 {
		t.Fatalf("the unrelated session was refused over content already passed (%d blocks)", n)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != asked {
		t.Fatalf("an unrelated session judging identical content asked the judge again (%d more call(s))", n-asked)
	}

	// Different content is a different fingerprint: the judge is asked again.
	e.Run(proj, "s-003-14-second", "add another", Turns("done",
		harness.CommitFile("c2", "docs/b.md", "more clean words", "add b"),
	))
	if n := e.JudgeCalls(proj, promptFile, ""); n <= asked {
		t.Fatalf("different content was not judged: the judge was asked %d time(s) in total", n)
	}
}

// T003_15: two conversations judging different content keep separate verdicts.
// Session one passes clean work; session two, unrelated, commits more and is
// refused by a failing judge over ITS range (the floor, both files). When session
// one next stops its range covers that same content, so it sees that content's
// verdict, replayed from the shared store; its earlier pass never excused session two.
func TestT003_15_TwoConversationsKeepVerdictsAndWatermarksApart(t *testing.T) {
	e, proj := judgeProject(t, verdictPass)
	e.Run(proj, "s-003-15-two", "look around", Turns("done", Bash("b0", "true")))

	e.Run(proj, "s-003-15-one", "write the doc", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
	))
	if n := stopBlocks(e, proj, "s-003-15-one"); n != 0 {
		t.Fatalf("premise: session one's clean commit was refused (%d blocks)", n)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("premise: the judge was asked %d times for session one; want 1", n)
	}

	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictFail)
	e.Run(proj, "s-003-15-two", "write another doc", Turns("done",
		harness.CommitFile("c2", "docs/b.md", "second doc", "add b"),
	))
	if !strings.Contains(strings.Join(e.BlockingErrorsFrom(proj, "s-003-15-two", "Stop"), "\n"), "JUDGE-SAYS-NO") {
		t.Fatalf("session two's range was not refused by the failing judge: %q", e.BlockingErrors(proj, "s-003-15-two"))
	}
	twoPrompt := e.JudgePrompt(proj, promptFile)
	if !strings.Contains(twoPrompt, "docs/a.md") || !strings.Contains(twoPrompt, "docs/b.md") {
		t.Fatalf("session two was judged without its whole range (both files):\n%s", twoPrompt)
	}
	before := e.JudgeCalls(proj, promptFile, "")

	// Session two's range held both files, a different fingerprint from session one's
	// earlier pass: it was judged on its own content (one fresh call), not waved
	// through on session one's pass.
	if before != 2 {
		t.Fatalf("session two's different content was not judged afresh: %d call(s) in total, want 2", before)
	}

	// Session one's range now covers the same content session two was judged over,
	// so it sees that content's verdict (replayed, the judge not asked again): each
	// Stop shows the verdict of its own range, and its earlier pass did not hold.
	e.Run(proj, "s-003-15-one", "check again", Turns("done", Bash("b1", "true")))
	if n := e.JudgeCalls(proj, promptFile, ""); n != before {
		t.Fatalf("session one's range holds content already judged but the judge was asked again (%d more call(s))", n-before)
	}
	if !strings.Contains(strings.Join(e.BlockingErrorsFrom(proj, "s-003-15-one", "Stop"), "\n"), "JUDGE-SAYS-NO") {
		t.Fatalf("session one's range over the refused content was not refused: %q", e.BlockingErrors(proj, "s-003-15-one"))
	}
}

// T003_16: a pass survives a re-fork. A fork is the same conversation under a new
// id and judges the same content: the stored judge pass is a cache hit and the
// judge is not asked again.
func TestT003_16_APassSurvivesAReFork(t *testing.T) {
	e, proj := judgeProject(t, verdictPass)

	e.Run(proj, "s-003-16-a", "write the doc", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
	))
	if n := stopBlocks(e, proj, "s-003-16-a"); n != 0 {
		t.Fatalf("premise: the clean commit was refused (%d blocks)", n)
	}
	asked := e.JudgeCalls(proj, promptFile, "")
	if asked == 0 {
		t.Fatal("premise: the judge was never asked")
	}

	e.RunForked(proj, "s-003-16-a", "s-003-16-b", "carry on", Turns("done", Bash("b1", "true")))
	if n := stopBlocks(e, proj, "s-003-16-b"); n != 0 {
		t.Fatalf("the fork was refused over a pass its conversation had earned (%d blocks)", n)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != asked {
		t.Fatalf("the forked session re-asked the judge about content already passed (%d more call(s))", n-asked)
	}
}

// T003_17: a refusal survives a re-fork. A failing judge's verdict recorded in one
// session is still refusing in a fork of it, and is replayed: the model is not
// asked again about input that has not changed.
func TestT003_17_ARefusalSurvivesAReFork(t *testing.T) {
	e, proj := judgeProject(t, verdictFail)

	e.Run(proj, "s-003-17-a", "write the doc", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a"),
	))
	if !strings.Contains(strings.Join(e.BlockingErrorsFrom(proj, "s-003-17-a", "Stop"), "\n"), "JUDGE-SAYS-NO") {
		t.Fatalf("premise: the failing judge did not refuse: %q", e.BlockingErrors(proj, "s-003-17-a"))
	}
	asked := e.JudgeCalls(proj, promptFile, "")
	if asked == 0 {
		t.Fatal("premise: the judge was never asked")
	}

	e.RunForked(proj, "s-003-17-a", "s-003-17-b", "carry on", Turns("done", Bash("b1", "true")))
	if !strings.Contains(strings.Join(e.BlockingErrorsFrom(proj, "s-003-17-b", "Stop"), "\n"), "JUDGE-SAYS-NO") {
		t.Fatalf("a refusal recorded before a re-fork did not refuse after it: %q", e.BlockingErrors(proj, "s-003-17-b"))
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != asked {
		t.Fatalf("the refusal was not replayed over the fork: the judge was asked %d more time(s)", n-asked)
	}
}
