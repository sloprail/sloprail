package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T005_01: a context activated THIS turn by a PostFileWrite is already active for
// a file-guard whose match reads it: the same Stop judges the committed changeset
// and refuses; a fix is judged and passes in the next.
func TestT005_01_FileGuardSeesAContextEnteredThisTurn(t *testing.T) {
	e, proj, led := setup(t, "1", false)

	e.Run(proj, "s-005-01", "start and write", Turns("done",
		Write("w1", "trigger.txt", "go\n"),
		harness.CommitFile("c1", "src/a.txt", "FORBIDDEN", "add a"),
	))
	errs := e.BlockingErrorsFrom(proj, "s-005-01", "Stop")
	if !strings.Contains(joined(errs), "SCOPED-GUARD refused") {
		t.Fatalf("the guard did not judge in the Stop that activated its context; blocking errors: %q", e.BlockingErrors(proj, "s-005-01"))
	}
	if ranCount(t, led) == 0 {
		t.Fatal("the guard's check never ran")
	}
	refused := len(errs)

	e.Run(proj, "s-005-01", "fix it", Turns("fixed",
		harness.CommitFile("c2", "src/a.txt", "clean", "fix a"),
	))
	if n := len(e.BlockingErrorsFrom(proj, "s-005-01", "Stop")); n != refused {
		t.Fatalf("the fix was refused again (%d refusals, had %d)", n, refused)
	}
}

// T005_02: commit-required honours a context-dependent match: a file selected only
// while the context is active is owed a commit in the SAME Stop that activates it.
func TestT005_02_CommitRequiredSeesAContextEnteredThisTurn(t *testing.T) {
	e, proj, _ := setup(t, "1", false)

	e.Run(proj, "s-005-02", "start and write", Turns("done",
		Write("w1", "trigger.txt", "go\n"),
		Write("w2", "src/b.txt", "uncommitted\n"),
	))
	owed := harness.CommitRequired(e.BlockingErrorsFrom(proj, "s-005-02", "Stop"))
	if len(owed) == 0 || !strings.Contains(owed[0], "src/b.txt") {
		t.Fatalf("an uncommitted file selected by a context-dependent match was not owed a commit in the activating Stop; blocking errors: %q", e.BlockingErrors(proj, "s-005-02"))
	}

	e.Run(proj, "s-005-02", "commit it", Turns("committed", harness.Commit("c1", "add b")))
	if n := len(harness.CommitRequired(e.BlockingErrorsFrom(proj, "s-005-02", "Stop"))); n != len(owed) {
		t.Fatalf("after committing, a commit was still required (%d, had %d)", n, len(owed))
	}
}

// T005_03: a Stop gate with require:[{context}] sees the context entered this
// turn: its check runs (and refuses) in that Stop, and passes once satisfied.
func TestT005_03_GateRequireSeesAContextEnteredThisTurn(t *testing.T) {
	e, proj, _ := setup(t, "1", true)

	e.Run(proj, "s-005-03", "start", Turns("done", Write("w1", "trigger.txt", "go\n")))
	errs := e.BlockingErrorsFrom(proj, "s-005-03", "Stop")
	if !strings.Contains(joined(errs), "STOP-GATE-RAN") {
		t.Fatalf("the gate's check did not run in the Stop that activated its required context; blocking errors: %q", e.BlockingErrors(proj, "s-005-03"))
	}
	refused := len(errs)

	e.Run(proj, "s-005-03", "approve", Turns("approved", Write("w2", "approved.txt", "ok\n")))
	if n := len(e.BlockingErrorsFrom(proj, "s-005-03", "Stop")); n != refused {
		t.Fatalf("the gate refused again after approval (%d, had %d)", n, refused)
	}
}

// T005_04: a context that EXITS at this Stop is still active for the gates of that
// same Stop — the gate refuses — and is inactive after it. Entering again (trigger
// rewritten) with the fixes lets the Stop pass. (File-guards no longer run at Stop:
// `sr check run` judges after the Stop, when the context has already exited.)
func TestT005_04_ContextExitingAtThisStopIsStillActiveForIt(t *testing.T) {
	e, proj, _ := setup(t, "0", true)

	e.Run(proj, "s-005-04", "start and write", Turns("done",
		Write("w1", "trigger.txt", "go\n"),
		harness.CommitFile("c1", "src/d.txt", "FORBIDDEN", "add d"),
	))
	got := joined(e.BlockingErrorsFrom(proj, "s-005-04", "Stop"))
	for _, want := range []string{"STOP-GATE-RAN"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the Stop in which the context exits did not see it active: missing %q in %q", want, e.BlockingErrors(proj, "s-005-04"))
		}
	}
	if active, _ := e.ContextState(proj, "s-005-04", "mode"); active {
		t.Fatal("the context did not close at the Stop it exits at")
	}
	refused := len(e.BlockingErrorsFrom(proj, "s-005-04", "Stop"))

	e.Run(proj, "s-005-04", "fix and re-enter", Turns("fixed",
		Write("w2", "trigger.txt", "again\n"),
		Write("w3", "approved.txt", "ok\n"),
		harness.CommitFile("c2", "src/d.txt", "clean", "fix d"),
	))
	if n := len(e.BlockingErrorsFrom(proj, "s-005-04", "Stop")); n != refused {
		t.Fatalf("the fixed work was refused again (%d refusals, had %d): %q", n, refused, e.BlockingErrors(proj, "s-005-04"))
	}
}

// T005_05: control. With the context inactive the context-scoped file-guard does
// not judge, however FORBIDDEN the commit; once the context activates, the same
// content is judged and refused.
func TestT005_05_InactiveContextMeansTheGuardDoesNotJudge(t *testing.T) {
	e, proj, led := setup(t, "1", false)

	e.Run(proj, "s-005-05", "commit without the mode", Turns("done",
		harness.CommitFile("c1", "src/e.txt", "FORBIDDEN", "add e"),
	))
	if errs := e.BlockingErrorsFrom(proj, "s-005-05", "Stop"); len(errs) != 0 {
		t.Fatalf("the guard judged with its context inactive: %q", errs)
	}
	if n := ranCount(t, led); n != 0 {
		t.Fatalf("the guard's check ran %d time(s) with its context inactive", n)
	}

	// (A Stop with nothing selected consumes the range, so the proof is a new
	// commit judged once the context is active.)
	e.Run(proj, "s-005-05", "now enter the mode", Turns("done",
		Write("w1", "trigger.txt", "go\n"),
		harness.CommitFile("c2", "src/f.txt", "FORBIDDEN", "add f"),
	))
	if !strings.Contains(joined(e.BlockingErrorsFrom(proj, "s-005-05", "Stop")), "SCOPED-GUARD refused") {
		t.Fatalf("with the context active the same content was not judged; blocking errors: %q", e.BlockingErrors(proj, "s-005-05"))
	}
}
