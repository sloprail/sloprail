package e2e

import "testing"

// T040_07: the tag-declared context's active/inactive state PERSISTS correctly
// into a SECOND Stop cycle of the SAME session — not just within the one cycle
// T040_04/05/06 each exercise.
//
// Cycle 1 declares #update with no artifact (T040_06's violation: refused,
// context stays active — exit.sh reads gates["verify-artifact-produced"].status,
// which is "fail"). Cycle 2, in the SAME session, produces the artifact the
// tag demanded (no NEW tag declared — the registry from cycle 1 persists). The
// context must STILL be active going into cycle 2's Stop, the gate must re-fire
// and find the tag now paired with an artifact, ADMIT, and the context must
// then DEACTIVATE.
func TestT040_07_FailedCycleStaysActiveThenArtifactCycleDeactivates(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-040-07"

	// ---- Cycle 1: declare #update, write nothing — refused, context stays active. ----
	e.Run(proj, sess, "claim an update, write nothing", Turns("done",
		Say("m1", "I'm calling this an update. #update"),
	))
	if active, _ := e.ContextState(proj, sess, "tag-declared"); !active {
		t.Fatalf("cycle 1: the context did not stay active after the gate refused a tag with no artifact — " +
			"the two-cycle assertion below would be meaningless if this did not hold first")
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) == 0 {
		t.Fatalf("cycle 1: the gate did not refuse the tag-without-artifact turn")
	}

	// ---- Cycle 2: produce the artifact the still-open #update demands. ----
	res := e.Run(proj, sess, "now write the update artifact", Turns("done",
		Write("w1", "memories/updates/note.md", "# An update\n"),
	))

	if status := e.GateState(proj, sess, "verify-artifact-produced"); status != "pass" {
		t.Fatalf("cycle 2: the verify-artifact-produced gate recorded %q after the artifact landed, want pass:\n%s", status, res.Output)
	}
	if active, _ := e.ContextState(proj, sess, "tag-declared"); active {
		t.Errorf("cycle 2: the tag-declared context stayed active after the gate passed — its exit should read the gate's pass and deactivate")
	}
}
