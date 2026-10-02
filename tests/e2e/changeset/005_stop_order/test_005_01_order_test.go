package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T005_01: a context activated THIS turn by a PostFileWrite is already active for
// a Stop gate whose match reads it: the same Stop runs the gate and it refuses; a
// fix is judged and passes in the next.
func TestT005_01_GateMatchSeesAContextEnteredThisTurn(t *testing.T) {
	e, proj, led := setup(t, "1", false)

	e.Run(proj, "s-005-01", "start and write", Turns("done",
		Write("w1", "trigger.txt", "go\n"),
		harness.CommitFile("c1", "src/a.txt", "FORBIDDEN", "add a"),
	))
	errs := e.BlockingErrorsFrom(proj, "s-005-01", "Stop")
	if !strings.Contains(joined(errs), "SCOPED-GATE refused") {
		t.Fatalf("the gate did not run in the Stop that activated its context; blocking errors: %q", e.BlockingErrors(proj, "s-005-01"))
	}
	if ranCount(t, led) == 0 {
		t.Fatal("the gate's check never ran")
	}
	refused := len(errs)

	e.Run(proj, "s-005-01", "fix it", Turns("fixed",
		harness.CommitFile("c2", "src/a.txt", "clean", "fix a"),
	))
	if n := len(e.BlockingErrorsFrom(proj, "s-005-01", "Stop")); n != refused {
		t.Fatalf("the fix was refused again (%d refusals, had %d)", n, refused)
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
// rewritten) with the fixes lets the Stop pass. The context-scoped gate runs in
// that same Stop too.
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

// T005_05: control. With the context inactive the context-scoped gate does not
// run, however FORBIDDEN the content; once the context activates, the same
// content is judged and refused.
func TestT005_05_InactiveContextMeansTheGateDoesNotRun(t *testing.T) {
	e, proj, led := setup(t, "1", false)

	e.Run(proj, "s-005-05", "commit without the mode", Turns("done",
		harness.CommitFile("c1", "src/e.txt", "FORBIDDEN", "add e"),
	))
	if errs := e.BlockingErrorsFrom(proj, "s-005-05", "Stop"); len(errs) != 0 {
		t.Fatalf("the gate ran with its context inactive: %q", errs)
	}
	if n := ranCount(t, led); n != 0 {
		t.Fatalf("the gate's check ran %d time(s) with its context inactive", n)
	}

	e.Run(proj, "s-005-05", "now enter the mode", Turns("done",
		Write("w1", "trigger.txt", "go\n"),
		harness.CommitFile("c2", "src/f.txt", "FORBIDDEN", "add f"),
	))
	if !strings.Contains(joined(e.BlockingErrorsFrom(proj, "s-005-05", "Stop")), "SCOPED-GATE refused") {
		t.Fatalf("with the context active the same content was not judged; blocking errors: %q", e.BlockingErrors(proj, "s-005-05"))
	}
}
