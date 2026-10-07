package e2e

import "testing"

// T037_10: the people-linked context's active/inactive state PERSISTS correctly
// into a SECOND Stop cycle of the SAME session — not just within the one cycle
// T037_05/07/08/09 each exercise.
//
// Cycle 1 creates an unlinked person (T037_05's violation: refused, context
// stays active — exit.sh reads gates["verify-linked"].status, which is "fail").
// Cycle 2, in the SAME session, links that person from a new update. The
// context must STILL be active going into cycle 2's Stop (nothing reset it
// between cycles), the gate must re-fire and find the now-added link, ADMIT,
// and the context must then DEACTIVATE — proving both halves the single-cycle
// tests cannot: that a failed cycle's "stay active" survives into the NEXT
// cycle, and that a later passing cycle correctly closes the scope rather than
// leaving it stuck open.
func TestT037_10_FailedCycleStaysActiveThenPassingCycleDeactivates(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.CommitAll(proj, "install")

	sess := "s-037-10"

	// ---- Cycle 1: create an unlinked person — refused, context stays active. ----
	e.Run(proj, sess, "add a person and forget to link them", Turns("done",
		Write("w1", "people/holly.md", "# Holly\nUnlinked."),
	))
	if active, _ := e.ContextState(proj, sess, "people-linked"); !active {
		t.Fatalf("cycle 1: the context did not stay active after the gate refused an unlinked person — " +
			"the two-cycle assertion below would be meaningless if this did not hold first")
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) == 0 {
		t.Fatalf("cycle 1: the gate did not refuse the unlinked person")
	}

	// ---- Cycle 2: link holly from a new update. ----
	res := e.Run(proj, sess, "link holly from an update", Turns("done",
		Write("w2", "updates/2026-08-21-note.md", "# Note\n\nCaught up with holly about the plan.\n"),
	))

	if status := e.GateState(proj, sess, "verify-linked"); status != "pass" {
		t.Fatalf("cycle 2: the verify-linked gate recorded %q after holly was linked, want pass:\n%s", status, res.Output)
	}
	if active, _ := e.ContextState(proj, sess, "people-linked"); active {
		t.Errorf("cycle 2: the people-linked context stayed active after the gate passed — its exit should read the gate's pass and deactivate")
	}
}
