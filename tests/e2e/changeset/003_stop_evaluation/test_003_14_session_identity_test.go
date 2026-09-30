package e2e

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Verdicts and watermarks belong to a CONVERSATION. Two sessions in one tree hold
// them apart; a fork of a conversation keeps them; an unrelated conversation
// starts with none.

// T003_14: an unrelated conversation inherits no verdict. The first session passes
// and its watermark holds (a Stop with nothing new runs no check); a second,
// unrelated session in the same tree has no watermark, so its first Stop judges the
// range from the rule's floor again.
func TestT003_14_AnUnrelatedSessionInheritsNoVerdicts(t *testing.T) {
	e, proj, led := project(t, docsRule)
	floor := e.Git(proj, "rev-parse", "HEAD~1")

	e.Run(proj, "s-003-14-first", "write the doc", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
	))
	if n := stopBlocks(e, proj, "s-003-14-first"); n != 0 {
		t.Fatalf("premise: the clean commit was refused (%d blocks)", n)
	}
	passed := len(ledger(t, led))
	if passed == 0 {
		t.Fatal("premise: the check never ran for the first session")
	}

	e.Run(proj, "s-003-14-first", "anything else?", Turns("no", Bash("b1", "true")))
	if n := len(ledger(t, led)); n != passed {
		t.Fatalf("the passing session's own watermark did not hold: %d more run(s)", n-passed)
	}

	e.Run(proj, "s-003-14-second", "look around", Turns("done", Bash("b1", "true")))
	runs := ledger(t, led)
	if len(runs) == passed {
		t.Fatal("an unrelated session took the first session's pass: the check did not run for it")
	}
	got := runs[len(runs)-1]
	if got.Base != floor || !reflect.DeepEqual(paths(got.Files), []string{"docs/a.md"}) {
		t.Fatalf("the unrelated session was judged over %+v; want the whole range from the floor %s", got, floor)
	}
}

// T003_15: two conversations in one tree keep their watermarks and verdicts apart.
// Session one passes clean work (its watermark moves); session two, unrelated,
// then commits a forbidden file and is refused over the range from the FLOOR. When
// session one next stops, its range starts at ITS watermark — only the new file —
// and is refused on its own evaluation, not by replaying session two's verdict.
func TestT003_15_TwoConversationsKeepVerdictsAndWatermarksApart(t *testing.T) {
	e, proj, led := project(t, docsRule)
	floor := e.Git(proj, "rev-parse", "HEAD~1")

	e.Run(proj, "s-003-15-one", "write the doc", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
	))
	passedHead := e.Git(proj, "rev-parse", "HEAD")
	if n := stopBlocks(e, proj, "s-003-15-one"); n != 0 {
		t.Fatalf("premise: session one's clean commit was refused (%d blocks)", n)
	}

	e.Run(proj, "s-003-15-two", "write another doc", Turns("done",
		harness.CommitFile("c2", "docs/b.md", "FORBIDDEN words", "add b"),
	))
	if !strings.Contains(strings.Join(e.BlockingErrorsFrom(proj, "s-003-15-two", "Stop"), "\n"), "FORBIDDEN text in the changeset") {
		t.Fatalf("session two's forbidden commit was not refused: %q", e.BlockingErrors(proj, "s-003-15-two"))
	}
	two := lastRun(t, led)
	if two.Base != floor || !reflect.DeepEqual(paths(two.Files), []string{"docs/a.md", "docs/b.md"}) {
		t.Fatalf("session two was judged over %+v; want the range from the floor %s holding both files", two, floor)
	}
	before := len(ledger(t, led))

	e.Run(proj, "s-003-15-one", "check again", Turns("done", Bash("b1", "true")))
	if len(ledger(t, led)) == before {
		t.Fatal("session one replayed session two's refusal instead of being evaluated: its verdicts are not its own")
	}
	one := lastRun(t, led)
	if one.Base != passedHead || !reflect.DeepEqual(paths(one.Files), []string{"docs/b.md"}) {
		t.Fatalf("session one was judged over %+v; want only the new file, from its own watermark %s", one, passedHead)
	}
	if n := stopBlocks(e, proj, "s-003-15-one"); n == 0 {
		t.Fatal("session one, handed the forbidden file, was not refused")
	}
}

// T003_16: a pass survives a re-fork. A fork is the same conversation under a new
// id, so the watermark the first session earned is still its own: nothing new
// means no check runs for the fork.
func TestT003_16_APassSurvivesAReFork(t *testing.T) {
	e, proj, led := project(t, docsRule)

	e.Run(proj, "s-003-16-a", "write the doc", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
	))
	if n := stopBlocks(e, proj, "s-003-16-a"); n != 0 {
		t.Fatalf("premise: the clean commit was refused (%d blocks)", n)
	}
	ran := len(ledger(t, led))
	if ran == 0 {
		t.Fatal("premise: the check never ran")
	}

	e.RunForked(proj, "s-003-16-a", "s-003-16-b", "carry on", Turns("done", Bash("b1", "true")))
	if n := len(ledger(t, led)); n != ran {
		t.Fatalf("the forked session judged work its conversation had already passed (%d more run(s))", n-ran)
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
