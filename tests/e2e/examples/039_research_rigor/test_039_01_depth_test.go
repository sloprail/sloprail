package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// noCloneReason is the depth gate's own wording when a research run shows no
// clone of its own — the words that must reach the agent.
const noCloneReason = "This #research run has not cloned a repository"

// whatToDo is what a depth refusal tells an agent that has cloned nothing.
const whatToDo = "To finish the research: git clone a real repository that implements what you are researching, then read at least 2 of its source files (not only the README or docs)"

// T039_01: a research run declared with #research is REFUSED at Stop when it
// shows no depth — nothing cloned, nothing read — and the refusal says what is
// missing AND what to do about it.
func TestT039_01_ShallowResearchRefused(t *testing.T) {
	e, proj := research(t)

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
	for _, want := range []string{noCloneReason, whatToDo, "Reads of directories this run did not clone do not count", "depth-check"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal is missing %q:\n%s", want, joined)
		}
	}
	// gh page counts are not part of the convention, so they are not asked for.
	if strings.Contains(joined, "gh") && strings.Contains(joined, "page") {
		t.Errorf("the refusal still asks for gh page coverage:\n%s", joined)
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
	e, proj := research(t)

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
// turn that declared no research is not subject to the depth check.
func TestT039_03_NoResearchNoGate(t *testing.T) {
	e, proj := research(t)

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

// readMore is what a depth refusal tells an agent that cloned but read too few
// source files: how many more, and where.
func readMore(n int, dirs string) string {
	files := "1 more distinct source file"
	if n != 1 {
		files = strconv.Itoa(n) + " more distinct source files"
	}
	return "To finish the research: read " + files + " inside " + dirs
}
