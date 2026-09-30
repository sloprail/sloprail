package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"strings"
	"testing"
)

// T002_01: uncommitted work on a guarded path refuses the Stop, in the blocking
// form the harness honours (the agent is driven on past it), naming the path and
// the rule; nothing is committed for the agent. Then the agent commits, and the
// same Stop passes.
func TestT002_01_UncommittedGuardedWorkRefusesTheStopUntilItIsCommitted(t *testing.T) {
	e, proj := project(t)
	before := e.Git(proj, "rev-parse", "HEAD")

	// Refusal: the agent writes a guarded file and stops.
	res := e.Run(proj, "s-002-01", "write the doc", Turns("done",
		Write("w1", "docs/new.md", "new doc\n"),
	))

	refused := harness.CommitRequired(e.BlockingErrorsFrom(proj, "s-002-01", "Stop"))
	if len(refused) == 0 {
		t.Fatalf("an uncommitted guarded file did not refuse the Stop; blocking errors: %q", e.BlockingErrors(proj, "s-002-01"))
	}
	for _, want := range []string{"docs/new.md", "file-guard/docs", "Sloprail-Cites-User"} {
		if !strings.Contains(refused[0], want) {
			t.Fatalf("the refusal should name %q:\n%s", want, refused[0])
		}
	}
	// The block HOLDS: the agent was driven on past its Stop, as the record shows
	// it, instead of being let end.
	if n := len(e.StopContinuations(proj, "s-002-01")); n < 1 {
		t.Fatalf("the refusal did not hold the turn; a Stop that does not block is a suggestion\n%s", res.Output)
	}
	if after := e.Git(proj, "rev-parse", "HEAD"); after != before {
		t.Fatal("the engine committed for the agent")
	}
	if e.Git(proj, "status", "--porcelain") == "" {
		t.Fatal("the work was staged or committed away by the engine")
	}

	// Pass: the agent commits what it wrote.
	seen := len(refused)
	e.Run(proj, "s-002-01", "now commit it", Turns("committed",
		Bash("c1", "git add -A && git commit -q -m 'add the doc'"),
	))
	if n := len(harness.CommitRequired(e.BlockingErrorsFrom(proj, "s-002-01", "Stop"))); n != seen {
		t.Fatalf("after committing, the Stop was refused again (%d refusals, had %d)", n, seen)
	}
}

// T002_02: only paths a rule SELECTS count. Scratch files and unguarded paths
// never trigger it, and neither does a clean tree.
func TestT002_02_UnguardedWorkNeverTriggersIt(t *testing.T) {
	e, proj := project(t)
	e.Run(proj, "s-002-02", "take notes", Turns("done",
		Write("w1", "notes/more.md", "notes\n"),
		Write("w2", "scratch.txt", "scratch\n"),
	))
	if errs := harness.CommitRequired(e.BlockingErrorsFrom(proj, "s-002-02", "Stop")); len(errs) != 0 {
		t.Fatalf("work no rule selects was refused: %q", errs)
	}
}

// T002_03: the kinds of change: a staged change counts, a modification counts, a
// deletion counts only when the rule admits deletions.
func TestT002_03_StatusesAndDeletions(t *testing.T) {
	// Default `deletions: skip`: an uncommitted deletion of a guarded file is
	// not selected.
	e, proj := project(t)
	e.Run(proj, "s-002-03a", "remove the seed", Turns("done", Bash("b1", "rm docs/seed.md")))
	if errs := harness.CommitRequired(e.BlockingErrorsFrom(proj, "s-002-03a", "Stop")); len(errs) != 0 {
		t.Fatalf("a deletion under `deletions: skip` was refused: %q", errs)
	}

	// `deletions: include`: the same deletion is now owed a commit.
	e2, proj2 := project(t)
	e2.FileGuard(proj2, "docs", rule+"deletions: include\n", map[string]string{"check.sh": passing})
	e2.CommitAll(proj2, "the rule admits deletions")
	e2.Run(proj2, "s-002-03b", "remove the seed", Turns("done", Bash("b1", "rm docs/seed.md")))
	errs := harness.CommitRequired(e2.BlockingErrorsFrom(proj2, "s-002-03b", "Stop"))
	if len(errs) == 0 || !strings.Contains(errs[0], "docs/seed.md") || !strings.Contains(errs[0], "deleted") {
		t.Fatalf("a deletion under `deletions: include` was not refused: %q", errs)
	}

	// A staged edit counts as much as an unstaged one.
	e3, proj3 := project(t)
	e3.Run(proj3, "s-002-03c", "edit and stage", Turns("done",
		Bash("b1", "echo more >> docs/seed.md && git add docs/seed.md"),
	))
	if errs := harness.CommitRequired(e3.BlockingErrorsFrom(proj3, "s-002-03c", "Stop")); len(errs) == 0 {
		t.Fatal("a staged modification of a guarded file was not refused")
	}
}

// T002_04: a project with no file-guards is never asked to commit.
func TestT002_04_NoFileGuardsNoCommitRequired(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Run(proj, "s-002-04", "write anything", Turns("done", Write("w1", "docs/x.md", "x\n")))
	if errs := harness.CommitRequired(e.BlockingErrorsFrom(proj, "s-002-04", "Stop")); len(errs) != 0 {
		t.Fatalf("refused with no file-guard declared: %q", errs)
	}
}
