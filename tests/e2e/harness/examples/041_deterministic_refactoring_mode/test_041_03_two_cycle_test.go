package e2e

import "testing"

// T041_09: the refactoring context's active/inactive state PERSISTS correctly
// into a SECOND Stop cycle of the SAME session — not just within the one cycle
// T041_07/08 each exercise, and directly proves the "Staying open across a
// failing Stop is what carries a multi-cycle refactor" design intent
// exit.sh's own doc comment states.
//
// Cycle 1 declares a move (scope=origin.go@sha:1-3) but writes nothing that
// carries the marker (T041_07's violation: the completeness gate blocks, and
// the context stays open — exit.sh reads gates["refactor-complete"].status,
// which is "fail" while declared_markers is non-empty). Cycle 2, in the SAME
// session, lands the declared move — WITHOUT re-declaring #refactor: enter.sh
// re-scans the WHOLE transcript's PostTagWrite history each PreToolUse
// occurrence, so cycle 1's declaration is what still populates
// declared_markers going into cycle 2. The completeness gate must re-fire,
// find the now-landed marker, PERMIT, and the context must then DEACTIVATE.
func TestT041_09_IncompleteCycleStaysActiveThenLandedCycleDeactivates(t *testing.T) {
	env, sha := setupOrigin(t)
	e, proj := env.e, env.proj

	sess := "s-041-09"

	// ---- Cycle 1: declare the move, write nothing that carries it. ----
	e.Run(proj, sess, "declare a move but never make it", Turns("done",
		SayWrite("w1", declRefactor(sha), "unrelated.md", "nothing to do with any marker"),
	).ThenCommit("write the files"))
	if active, payload := e.ContextState(proj, sess, "refactoring"); !active {
		t.Fatalf("cycle 1: the refactoring context did not stay active after the completeness gate blocked — "+
			"the two-cycle assertion below would be meaningless if this did not hold first (payload=%v)", payload)
	}
	blocks1 := e.BlockingErrorsFrom(proj, sess, "Stop")
	sawCompleteness := false
	for _, b := range blocks1 {
		if containsAny(b, completenessRefusal, "never landed") {
			sawCompleteness = true
		}
	}
	if !sawCompleteness {
		t.Fatalf("cycle 1: the completeness gate did not block the declared-but-unwritten refactor: %v", blocks1)
	}

	// ---- Cycle 2: land the declared move, with NO new #refactor declaration. ----
	moved := "// sr:moved-from origin.go@" + sha + ":1-3\nfunc Beta() int {\n\treturn 1\n}\n"
	res := e.Run(proj, sess, "now make the declared move", Turns("done",
		Write("w2", "dest.go", moved),
	).ThenCommit("write the files"))

	if res.Refused() {
		t.Fatalf("cycle 2: the reconciling declared move was refused at PreToolUse:\n%s", res.Output)
	}
	if !e.Exists(proj, "dest.go") {
		t.Fatalf("cycle 2: the declared move did not land")
	}
	if status := e.GateState(proj, sess, "refactor-complete"); status != "pass" {
		t.Fatalf("cycle 2: the refactor-complete gate recorded %q after the move landed, want pass:\n%s", status, res.Output)
	}
	if active, _ := e.ContextState(proj, sess, "refactoring"); active {
		t.Errorf("cycle 2: the refactoring context stayed active after the completeness gate passed — its exit should read the gate's pass and deactivate")
	}
}
