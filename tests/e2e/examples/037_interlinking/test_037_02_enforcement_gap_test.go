package e2e

import (
	"testing"
)

// T037_05: a created-but-UNLINKED person is NOT refused — the enforcement is a
// silent no-op because the gate cannot read the context's registry.
//
// This PINS the blocking example bug. A person is created and linked NOWHERE under
// updates/decisions — the clear "created-but-unlinked" violation the unit exists
// to catch. The context logs people/dave.md correctly (asserted, so we know the
// setup is a real violation and not an empty turn), but the gate's check runs
// `sr-session state list --owner people-linked`, `--owner` is not a real flag, the
// read comes back empty, and the gate passes. So the Stop is admitted.
//
// The test asserts the CURRENT (broken) outcome: no refusal, despite a genuine
// unlinked person. If the example is fixed to read the context payload (and anchor
// its greps on $SR_WORKSPACE), this violation would then be refused and THIS test
// must be updated to assert the refusal + its "Interlinking check failed" reason.
func TestT037_05_CreatedButUnlinkedNotRefused_ExampleBug(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-037-05"
	// Create a person, link them nowhere. A real created-but-unlinked violation.
	res := e.Run(proj, sess, "add a person and forget to link them", Turns("done",
		Write("w1", "people/dave.md", "# Dave\nUnlinked."),
	))

	// The context DID log the person — so this is a genuine violation the gate
	// should have caught, not an empty turn.
	reg := e.GuardrailState(proj, sess, "people-linked", "")
	if _, ok := reg["people/dave.md"]; !ok {
		t.Fatalf("precondition: the context did not log the person, so this is not a real "+
			"unlinked-violation setup; registry=%v", reg)
	}

	// CURRENT behavior: the gate does not refuse — it read an empty registry
	// (`--owner` is rejected) and passed.
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) != 0 {
		t.Fatalf("the interlinking gate REFUSED a created-but-unlinked person — the enforcement "+
			"may have been fixed (gate now reads the context payload). Update this test to assert "+
			"the refusal.\nblocks=%v", blocks)
	}
	if res.Refused() {
		t.Errorf("unexpected refusal:\n%s", res.Output)
	}
}

// T037_06: the gate REFUSES a turn that touched no people file at all — because
// it lacks a `match` and relies on `require: context` alone, and an unmet context
// require BLOCKS rather than skips.
//
// This PINS a third interlinking bug. The gate is `on: Stop` with NO `match` and
// `require: context: people-linked`. Its own comment expects the require to make
// the gate "only run once we know which people/*.md changed" — i.e. to SKIP when
// the context is inactive. But an unmet `{context}` prerequisite is a REFUSAL in
// the engine ("this rule requires the people-linked context to be active first"),
// not a skip. So the gate blocks EVERY turn that does not touch a people file —
// all unrelated work. The sibling gates (keyword-coverage, research-rigor) avoid
// this by adding `match: context["...-..."].active`, which makes the gate only
// FIRE when the context is active; interlinking omits it.
//
// The test asserts the CURRENT (broken) outcome: an unrelated turn is refused with
// the require message. If the gate is fixed to add the `.active` match, this turn
// would pass and THIS test must flip to assert no refusal.
func TestT037_06_GateBlocksUnrelatedTurns_ExampleBug(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-037-06"
	res := e.Run(proj, sess, "do work unrelated to people", Turns("done",
		Write("w1", "updates/note.md", "an update mentioning nobody in particular"),
	))

	// The context did NOT activate (no people touch) — so the gate's require is
	// unmet.
	if active, _ := e.ContextState(proj, sess, "people-linked"); active {
		t.Errorf("the context activated on a non-people write, which would change what this test proves")
	}
	// CURRENT behavior: the unmet context require REFUSES the Stop.
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("EXPECTED the gate to refuse an unrelated turn (unmet context require blocks, and "+
			"the gate has no `match` to skip on). It did not — the gate may have gained a "+
			"`match: context[...].active`; if so, update this test to assert no refusal.\n%s", res.Output)
	}
	if !containsAny(joinBlocks(blocks), "requires the \"people-linked\" context to be active") {
		t.Errorf("refused, but not with the unmet-context-require reason:\n%v", blocks)
	}
}

func joinBlocks(bs []string) string {
	out := ""
	for _, b := range bs {
		out += b + "\n"
	}
	return out
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) == 0 {
			continue
		}
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
