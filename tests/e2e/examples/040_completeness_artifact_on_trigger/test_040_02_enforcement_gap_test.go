package e2e

import (
	"strings"
	"testing"
)

// falseRefusal is verify-artifact-produced's wording — which, because the check
// cannot read the registry, reaches the agent even when the artifact WAS produced.
const falseRefusal = "no matching artifact was produced this turn"

// T040_04: a turn that declares #update AND produces the update artifact is STILL
// refused by verify-artifact-produced — the gate cannot read the registry, so it
// wrongly concludes no artifact was produced.
//
// This PINS the blocking example bug, which here manifests as a FALSE REFUSAL
// (worse than a silent pass). verify-artifact-produced's check runs `sr-session
// state list --owner tag-declared`; `--owner` is rejected, the read is empty, so
// `tags` is empty (not "skip") and the artifact count is 0 — and the check refuses
// "Turn declared a tag () but no matching artifact was produced". But the artifact
// WAS produced (the context logged both tag:update and artifact:..., asserted
// below). So a legitimate, complete turn is refused.
//
// The test asserts the CURRENT (broken) outcome: the tagged+artifacted turn is
// refused with the false "no matching artifact" reason. If the gate is fixed to
// read the context payload, this turn would admit and THIS test must flip.
func TestT040_04_TagWithArtifactFalselyRefused_ExampleBug(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-040-04"
	e.Run(proj, sess, "record a complete update", Turns("done",
		SayWrite("w1", "Recording this. #update", "memories/updates/note.md", "# An update\n"),
	))

	// The turn IS complete — both the tag and the artifact were logged.
	reg := e.GuardrailState(proj, sess, "tag-declared", "")
	if _, ok := reg["tag:update"]; !ok {
		t.Fatalf("precondition: the #update tag was not logged; registry=%v", reg)
	}
	if _, ok := reg["artifact:memories/updates/note.md"]; !ok {
		t.Fatalf("precondition: the artifact was not logged; registry=%v", reg)
	}

	// CURRENT behavior: refused anyway, with the false "no matching artifact"
	// reason (the check could not read the registry).
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, falseRefusal) {
		t.Fatalf("EXPECTED a FALSE refusal on a complete tagged+artifacted turn (the gate cannot "+
			"read the registry via --owner). It did not appear — the gate may have been fixed to read "+
			"the context payload; if so, update this test to assert the turn ADMITS.\nblocks=%v", blocks)
	}
}

// T040_05: a turn that declares #skip — which needs NO artifact — is STILL refused
// by verify-artifact-produced.
//
// The #skip admit path is broken by the same bug. #skip means "this turn needs no
// artifact" and should pass verify-artifact-produced via its `grep -qx "skip"`
// early exit. But that grep runs over `$tags`, which the check reads from the
// unreadable `--owner` registry as EMPTY — so the skip is invisible, the artifact
// count is 0, and the check refuses. A turn that explicitly opted out of producing
// an artifact is refused for not producing one.
//
// The test asserts the CURRENT (broken) outcome: #skip is refused with the false
// reason. If fixed, #skip would admit and THIS test must flip.
func TestT040_05_SkipFalselyRefused_ExampleBug(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-040-05"
	res := e.Run(proj, sess, "declare a skip", Turns("done",
		Say("m1", "Nothing worth recording here. #skip"),
	))

	// The context DID log the skip tag — so the setup is a real #skip.
	if _, ok := e.GuardrailState(proj, sess, "tag-declared", "")["tag:skip"]; !ok {
		t.Fatalf("precondition: the #skip tag was not logged")
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if !strings.Contains(strings.Join(blocks, "\n"), falseRefusal) {
		t.Fatalf("EXPECTED #skip to be FALSELY refused (the check cannot see the skip tag via "+
			"--owner). It was not — the gate may have been fixed; if so, update this test to assert "+
			"#skip ADMITS.\nblocks=%v\noutput=%s", blocks, res.Output)
	}
}
