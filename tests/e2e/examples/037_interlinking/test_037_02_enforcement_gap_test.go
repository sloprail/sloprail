package e2e

import (
	"strings"
	"testing"
)

// This file drives the interlinking gate's ENFORCEMENT, now that it reads the
// people-linked context's registry via `state list --owner people-linked` and is
// scoped by `match: context["people-linked"].active`. It covers the
// created-but-unlinked violation, the linked-person admit, the no-dangling-link
// deletion paths, and the control that the gate no longer blocks unrelated turns.

// linkRefusal is the gate's own wording for an unlinked/dangling person.
const linkRefusal = "Interlinking check failed"

// T037_05: a created-but-UNLINKED person is REFUSED at Stop, and the gate's own
// reason (naming the offending path) reaches the agent.
//
// The core violation the unit exists to catch: a person file that no update or
// decision links to. The context logs people/dave.md; the gate reads that registry
// via --owner, finds dave is referenced nowhere under updates/decisions, and
// refuses.
func TestT037_05_CreatedButUnlinkedRefused(t *testing.T) {
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

	// The context DID log the person — so this is a genuine violation, not an
	// empty turn.
	reg := e.GuardrailState(proj, sess, "people-linked", "")
	if _, ok := reg["people/dave.md"]; !ok {
		t.Fatalf("precondition: the context did not log the person; registry=%v", reg)
	}

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("the interlinking gate did not refuse a created-but-unlinked person:\n%s", res.Output)
	}
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, linkRefusal) {
		t.Errorf("the gate's link-check reason did not reach the agent:\n%s", joined)
	}
	// The offending path is named, so the agent knows WHO to link.
	if !strings.Contains(joined, "people/dave.md") {
		t.Errorf("the refusal did not name the unlinked person:\n%s", joined)
	}
	if !strings.Contains(joined, "verify-linked") {
		t.Errorf("the refusal did not name the gate:\n%s", joined)
	}
}

// T037_07: a created person WHO IS LINKED from an update ADMITS — the control that
// proves T037_05's refusal is conditional, and that the --owner registry read plus
// the $SR_WORKSPACE-anchored grep actually find the link.
//
// An update file under updates/ mentions the person by name (its file stem), so
// the gate's `grep -rlq "$name" "$SR_WORKSPACE/updates" ...` finds a link and the
// person is accounted for. The update is committed at the baseline; the person is
// created this cycle.
func TestT037_07_LinkedPersonAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	// An update that links to "erin" already exists in the tree.
	e.WriteFile(proj, "updates/2026-08-20-standup.md", "# Standup\n\nCaught up with erin about the plan.\n")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install + update linking erin")

	sess := "s-037-07"
	res := e.Run(proj, sess, "add erin, already linked", Turns("done",
		Write("w1", "people/erin.md", "# Erin\nLinked from the standup update."),
	))

	// The context logged erin (a real subject the gate checked), and the gate
	// admitted because the update links to her.
	if _, ok := e.GuardrailState(proj, sess, "people-linked", "")["people/erin.md"]; !ok {
		t.Fatalf("precondition: erin was not logged by the context")
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("the gate refused a person who IS linked from an update:\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("unexpected refusal:\n%s", res.Output)
	}
}

// T037_08: a DELETED person who is still referenced from an update is REFUSED
// (a dangling link).
//
// The deletion half of the unit. A person committed at the baseline, plus an
// update that mentions them by stem, is removed with `rm`: the context logs the
// delete, the gate finds a dangling reference under updates/ and refuses. Its
// control — deleting a person nobody references — is T037_09.
func TestT037_08_DeletedPersonDanglingRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	// A person AND an update that references them by file stem, both at the
	// baseline. The reference matches the stem ("frank") the gate greps for.
	e.WriteFile(proj, "people/frank.md", "# Frank")
	e.WriteFile(proj, "updates/note.md", "# Note\n\nOwnership: frank drives the rollout.\n")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install + frank(referenced)")

	// Deleting frank leaves a dangling link → refused.
	sess := "s-037-08a"
	res := e.Run(proj, sess, "remove frank", Turns("done",
		Bash("d1", "rm people/frank.md"),
	))
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("deleting a still-referenced person was not refused:\n%s", res.Output)
	}
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, linkRefusal) || !strings.Contains(joined, "people/frank.md") {
		t.Errorf("the dangling-link refusal did not name frank with the link reason:\n%s", joined)
	}
}

// T037_09: deleting a person NOBODY references ADMITS — the control for T037_08,
// proving the deletion check refuses only on an actual dangling link.
//
// A fresh project (not the T037_08 one) so no other deletion leaks into this
// cycle's tree diff: grace is committed at the baseline, referenced nowhere, then
// removed. The context logs the delete; the gate finds no dangling reference and
// admits.
func TestT037_09_DeletedUnreferencedPersonAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.WriteFile(proj, "people/grace.md", "# Grace")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install + grace(unreferenced)")

	sess := "s-037-09"
	res := e.Run(proj, sess, "remove grace", Turns("done",
		Bash("d1", "rm people/grace.md"),
	))

	// The delete WAS logged (a real subject the gate checked), and it admitted.
	if _, ok := e.GuardrailState(proj, sess, "people-linked", "")["people/grace.md"]; !ok {
		t.Fatalf("precondition: the delete of grace was not logged by the context")
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("deleting an unreferenced person was refused:\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("unexpected refusal:\n%s", res.Output)
	}
}

// T037_06: the gate does NOT block a turn that touched no people file — its
// `match: context["people-linked"].active` skips the check when the context is
// inactive.
//
// The control that proves the gate is properly scoped: with no person changed the
// context is inactive, the gate's match is false, its check never runs, and the
// unrelated turn is admitted. (Before the fix this gate had no match and its unmet
// context require REFUSED every unrelated turn.)
func TestT037_06_UnrelatedTurnNotBlocked(t *testing.T) {
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

	if active, _ := e.ContextState(proj, sess, "people-linked"); active {
		t.Errorf("the context activated on a non-people write, which would change what this test proves")
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("the gate blocked an unrelated turn (its match should skip when no person changed):\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("unexpected refusal on an unrelated turn:\n%s", res.Output)
	}
}
