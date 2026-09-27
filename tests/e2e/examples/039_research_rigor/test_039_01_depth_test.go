package e2e

import (
	"strings"
	"testing"
)

// noCloneReason is check 1's own wording — the words that must reach the agent
// when a research run shows no real clone.
const noCloneReason = "No git clone found in this research run"

// T039_01: a research run declared with #research is REFUSED at Stop when it shows
// no depth (no git clone, no gh calls), and both reasons reach the agent at once.
//
// The core of the guardrail: declaring a research branch (#research) puts the run
// under the depth gate, which refuses a shallow run. This drives the activate →
// gate → refuse path end to end.
func TestT039_01_ShallowResearchRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-039-01"
	res := e.Run(proj, sess, "look into this", Turns("done",
		Say("m1", "Digging into the repo as a #research task."),
	))

	// The context activated on the tag.
	if active, _ := e.ContextState(proj, sess, "research-run"); !active {
		t.Fatalf("the research-run context did not activate on the #research tag")
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("the depth gate did not refuse a shallow research run:\n%s", res.Output)
	}
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, noCloneReason) {
		t.Errorf("the depth gate's own reason did not reach the agent:\n%s", joined)
	}
	// Everything the run still owes arrives in one refusal, not one fact per Stop.
	if !strings.Contains(joined, "No gh CLI calls found") {
		t.Errorf("the refusal did not also name the missing gh calls:\n%s", joined)
	}
	if !strings.Contains(joined, "depth-check") {
		t.Errorf("the refusal did not name the gate:\n%s", joined)
	}
}

// T039_02: the context does NOT activate on a DIFFERENT tag — the trigger narrows
// to #research.
//
// The control for T039_01: the agent writes #planning, not #research, so the
// context's `match: any(event.tags, .label == "research")` excludes it and the
// depth gate never runs. Proves the guardrail keys on the research declaration,
// not on any tag.
func TestT039_02_OtherTagDoesNotActivate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-039-02"
	res := e.Run(proj, sess, "plan something", Turns("done",
		Say("m1", "Sketching an approach as a #planning note."),
	))

	if active, _ := e.ContextState(proj, sess, "research-run"); active {
		t.Errorf("the context activated on a tag its match should have excluded")
	}
	// And with the context inactive, the depth gate does not run — no refusal.
	if len(e.BlockingErrorsFrom(proj, sess, "Stop")) != 0 {
		t.Errorf("the depth gate refused a non-research turn:\n%s", res.Output)
	}
}

// T039_03: with NO tag at all, the depth gate does not run — an ordinary turn is
// untouched.
//
// The second control: `match: context["research-run"].active` on the gate means a
// turn that declared no research is not subject to the depth check. An ordinary
// file write with no #research passes.
func TestT039_03_NoResearchNoGate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-039-03"
	res := e.Run(proj, sess, "do ordinary work", Turns("done",
		Say("m1", "Just noting something, no research here."),
	))

	if active, _ := e.ContextState(proj, sess, "research-run"); active {
		t.Errorf("the research context activated with no #research tag")
	}
	if res.Refused() || len(e.BlockingErrorsFrom(proj, sess, "Stop")) != 0 {
		t.Errorf("an ordinary non-research turn was refused:\n%s", res.Output)
	}
}
