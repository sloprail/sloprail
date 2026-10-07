package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// T039_07/08: the research-run CONTEXT'S own active/inactive state PERSISTS
// correctly into a SECOND Stop cycle of the SAME session — not just within the
// one cycle T039_01/T039_04 each exercise.
//
// Before this file, every test in this suite ran exactly one Stop cycle per
// session. T039_04 already proves the context deactivates in the SAME cycle its
// depth gate passes, and T039_01 proves it stays active when the gate fails in
// that cycle — but nobody proved the state that exit.sh set actually survives
// into a LATER, separate cycle: does a context that deactivated after passing
// really stop re-triggering depth-check on an unrelated later turn (rather than,
// say, re-entering because some state leaked), and does a context that stayed
// active after failing really keep the gate armed on the NEXT turn (rather than,
// say, silently resetting because nothing re-declared #research)? The gate's own
// match (`context["research-run"].active`) and `require` (context runs first)
// make this a real two-cycle question, not a restatement of the single-cycle
// tests.

// T039_07: cycle 1 is a DEEP research run (passes, context deactivates — same
// setup as T039_04). Cycle 2, in the SAME session, is an ORDINARY unrelated turn
// with no #research tag. The depth gate must NOT re-fire — the context's
// deactivation from cycle 1 must still hold, not merely have been true
// momentarily within cycle 1's own Stop.
func TestT039_07_DeactivatedContextStaysInactiveNextCycle(t *testing.T) {
	e, proj := research(t)

	sess := "s-039-07"

	// Cycle 1: deep research — passes, deactivates (T039_04's scenario).
	src := sourceRepo(t, e, "retry-lib")
	dst := filepath.Join(scratch(t), "retry-lib")
	e.Run(proj, sess, "deep research", Turns("done",
		SayBash("b1", "Cloning to study it. #research", "git clone "+src+" "+dst),
		Read("r1", filepath.Join(dst, "lib", "retry.js")),
		Read("r2", filepath.Join(dst, "lib", "backoff.js")),
	))
	if active, _ := e.ContextState(proj, sess, "research-run"); active {
		t.Fatalf("cycle 1: the research context did not deactivate after the depth gate passed — " +
			"the two-cycle assertion below would be meaningless if this did not hold first")
	}

	// Cycle 2: unrelated work, no #research tag, no clone, no reads — exactly
	// what a shallow research run would ALSO look like. If the context's
	// inactivity did not persist, this would be wrongly refused by depth-check.
	res := e.Run(proj, sess, "do something unrelated", Turns("done",
		Say("m1", "Just leaving a note, nothing to do with research."),
	))

	if active, _ := e.ContextState(proj, sess, "research-run"); active {
		t.Errorf("cycle 2: the research context re-activated on an unrelated turn with no #research tag")
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("cycle 2: the depth gate re-fired on an unrelated turn after the context had "+
			"deactivated in cycle 1 — its deactivation did not persist:\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("cycle 2: an unrelated turn was refused:\n%s", res.Output)
	}
}

// T039_08: cycle 1 is a SHALLOW research run (fails, context stays active — same
// setup as T039_01). Cycle 2, in the SAME session, is an unrelated turn that
// declares no new #research tag. The context must STAY active and the depth
// gate must fire AGAIN — the failure from cycle 1 is not forgotten just because
// the next turn did not re-declare research; the run is still open and still
// shallow until it is fixed or abandoned.
func TestT039_08_ActiveContextStaysActiveAndReFiresNextCycle(t *testing.T) {
	e, proj := research(t)

	sess := "s-039-08"

	// Cycle 1: shallow research — refused, context stays active (T039_01's
	// scenario).
	e.Run(proj, sess, "look into this", Turns("done",
		Say("m1", "Digging into the repo as a #research task."),
	))
	if active, _ := e.ContextState(proj, sess, "research-run"); !active {
		t.Fatalf("cycle 1: the research context did not activate/stay active on a shallow run — " +
			"the two-cycle assertion below would be meaningless if this did not hold first")
	}

	// Cycle 2: an unrelated turn, no new #research tag — the context's `on:` only
	// triggers ENTER on a fresh #research PostTagWrite, so this turn contributes
	// no new activation. The still-open, still-shallow run from cycle 1 is what
	// must keep the gate armed.
	res := e.Run(proj, sess, "do something else, still without fixing the research", Turns("done",
		Say("m2", "A completely unrelated note."),
	))

	if active, _ := e.ContextState(proj, sess, "research-run"); !active {
		t.Errorf("cycle 2: the research context went inactive on its own, with no passing depth check " +
			"and no new #research declaration — its stay-active state did not persist")
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("cycle 2: the depth gate did not re-fire for the still-open shallow research run:\n%s", res.Output)
	}
	if !strings.Contains(strings.Join(blocks, "\n"), noCloneReason) {
		t.Errorf("cycle 2: the re-fired refusal did not carry the depth gate's own reason:\n%v", blocks)
	}
}
