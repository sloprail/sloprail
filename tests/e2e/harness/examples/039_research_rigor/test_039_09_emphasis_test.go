package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// This file covers a #research declaration written in markdown emphasis (issue
// #89). A real eval run closed with `**#research summary:**`; the engine read no
// tag in it, research-run never activated, and neither depth gate ran — a
// shallow run went unjudged. Emphasis around the tag is the tag.

// T039_55: a shallow run that declares `**#research summary:**` in bold is
// REFUSED at Stop, with the depth gate's own reason — the context activated on
// the emphasised tag and the gate judged it.
//
// On origin/main the context stayed inactive and the Stop passed.
func TestT039_55_BoldResearchDeclarationActivatesTheGates(t *testing.T) {
	e, proj := research(t)

	sess := "s-039-55"
	res := e.Run(proj, sess, "research retry", Turns("done",
		Say("m1", "Done. **#research summary:** backoff with jitter, capped at 30s."),
	))

	if active, _ := e.ContextState(proj, sess, "research-run"); !active {
		t.Fatalf("the research-run context did not activate on **#research summary:**:\n%s", res.Output)
	}
	joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	for _, want := range []string{noCloneReason, whatToDo, "depth-check"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the Stop refusal is missing %q:\n%s", want, joined)
		}
	}
}

// T039_56: the control — the same bold declaration on a DEEP run (a clone and
// two of its source files read) is admitted, and the context closes. The
// refusal in T039_55 is the depth, not the emphasis.
func TestT039_56_BoldResearchDeclarationWithDepthAdmits(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	dst := filepath.Join(scratch(t), "retry-lib")

	sess := "s-039-56"
	res := e.Run(proj, sess, "research retry", Turns("done",
		SayBash("b1", "Cloning to study it.", "git clone "+src+" "+dst),
		Read("r1", filepath.Join(dst, "lib", "retry.js")),
		Read("r2", filepath.Join(dst, "lib", "backoff.js")),
		Say("m1", "**#research summary:** backoff with jitter, capped at 30s."),
	))

	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("a deep research run declared in bold was refused:\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("unexpected refusal:\n%s", res.Output)
	}
	if st := e.GateState(proj, sess, "depth-check"); st != "pass" {
		t.Errorf("the depth gate recorded %q, want pass — it should have run on the bold declaration", st)
	}
}
