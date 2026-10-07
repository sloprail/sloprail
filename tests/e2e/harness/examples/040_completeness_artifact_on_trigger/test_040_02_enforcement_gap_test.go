package e2e

import (
	"strings"
	"testing"
)

// This file drives verify-artifact-produced's enforcement, now that it reads the
// tag-declared context's registry via `state list --owner tag-declared`: a tag
// with its artifact ADMITS, #skip ADMITS (no artifact required), and a tag WITHOUT
// its artifact is REFUSED.

// missingArtifactReason is the gate's own wording for a tag with no artifact.
const missingArtifactReason = "no matching artifact was produced this turn"

// T040_04: a turn that declares #update AND produces the update artifact ADMITS.
//
// The completeness happy path: the tag was stated and the artifact it demands
// landed. The context logs both tag:update and artifact:...; the gate reads them
// via --owner, sees an artifact for the tag, and admits. (The sibling tag-required
// gate skips — its `not active` match is false while a tag is declared.)
func TestT040_04_TagWithArtifactAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.CommitAll(proj, "install")

	sess := "s-040-04"
	res := e.Run(proj, sess, "record a complete update", Turns("done",
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

	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("a complete #update+artifact turn was refused:\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("unexpected refusal:\n%s", res.Output)
	}
}

// T040_05: a turn that declares #skip — which needs NO artifact — ADMITS.
//
// The declarative "needs no artifact" path: #skip is the agent saying this turn
// produces nothing to record. The gate reads the tag set via --owner, sees "skip",
// and admits via its skip early-exit without demanding an artifact.
func TestT040_05_SkipAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.CommitAll(proj, "install")

	sess := "s-040-05"
	res := e.Run(proj, sess, "declare a skip", Turns("done",
		Say("m1", "Nothing worth recording here. #skip"),
	))

	// The context DID log the skip tag — so the setup is a real #skip.
	if _, ok := e.GuardrailState(proj, sess, "tag-declared", "")["tag:skip"]; !ok {
		t.Fatalf("precondition: the #skip tag was not logged")
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("a #skip turn (which needs no artifact) was refused:\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("unexpected refusal:\n%s", res.Output)
	}
}

// T040_06: a turn that declares #update but produces NO artifact is REFUSED, and
// the gate's own reason reaches the agent.
//
// The core violation verify-artifact-produced exists to catch: a tag stated
// without the artifact it demands. The context logs tag:update with no artifact
// entry; the gate reads the registry via --owner, finds a non-skip tag and zero
// artifacts, and refuses.
//
// The #update is declared in a pure-text turn (no file write), so no artifact
// lands. A fresh project ensures no earlier turn's artifact leaks into the tree
// diff.
func TestT040_06_TagWithoutArtifactRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.CommitAll(proj, "install")

	sess := "s-040-06"
	res := e.Run(proj, sess, "claim an update, write nothing", Turns("done",
		Say("m1", "I'm calling this an update. #update"),
	))

	// The tag was logged, and no artifact was — a real incomplete turn.
	reg := e.GuardrailState(proj, sess, "tag-declared", "")
	if _, ok := reg["tag:update"]; !ok {
		t.Fatalf("precondition: the #update tag was not logged; registry=%v", reg)
	}
	for k := range reg {
		if strings.HasPrefix(k, "artifact:") {
			t.Fatalf("precondition: an artifact was logged (%s), so this is not a no-artifact turn; registry=%v", k, reg)
		}
	}

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a #update turn with no artifact was not refused:\n%s", res.Output)
	}
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, missingArtifactReason) {
		t.Errorf("the missing-artifact reason did not reach the agent:\n%s", joined)
	}
	if !strings.Contains(joined, "verify-artifact-produced") {
		t.Errorf("the refusal did not name the gate:\n%s", joined)
	}
}
