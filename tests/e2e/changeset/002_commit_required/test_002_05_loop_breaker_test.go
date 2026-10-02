package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"strings"
	"testing"
)

// T002_05: the loop breaker. After stop_hook_block_cap refusals in a row for the
// SAME uncommitted set the gate says so and stops refusing; a different set
// starts the count again; committing ends it. Driven by calling `sr-session
// stop` the way the harness does, so each Stop is exact. Each is sent as a fresh
// Stop (stop_hook_active false), so it is THIS gate's own counter, keyed on the
// set, that ends the loop — the Stop-wide count of consecutive refusals resets
// on every fresh Stop and cannot be what released it.
func TestT002_05_LoopBreakerReleasesAfterTheCapForTheSameSet(t *testing.T) {
	e, proj := project(t)
	e.WriteFile(proj, ".sloprail/config.yaml", "stop_hook_block_cap: 2\n")
	e.CommitAll(proj, "cap the refusal loop at two")
	e.Run(proj, "s-002-05", "hello", Turns("done", Bash("b1", "true")))

	e.WriteFile(proj, "docs/a.md", "a\n")

	// The first two Stops are refused: the loop the cap bounds.
	for i := 1; i <= 2; i++ {
		r := e.StopNow(proj, "s-002-05", false)
		if !harness.Blocked(r) || !strings.Contains(r.Output, "Commit your work") {
			t.Fatalf("Stop %d for the same set was not refused:\n%s", i, r.Output)
		}
	}
	// The third is released — out loud.
	r := e.StopNow(proj, "s-002-05", false)
	if harness.Blocked(r) {
		t.Fatalf("the loop breaker did not release the third refusal:\n%s", r.Output)
	}
	if !strings.Contains(r.Output, "stop_hook_block_cap") || !strings.Contains(r.Output, "uncommitted") {
		t.Fatalf("the release should say what happened:\n%s", r.Output)
	}

	// A different set is a different question: refused again, from a fresh count.
	e.WriteFile(proj, "docs/b.md", "b\n")
	if r := e.StopNow(proj, "s-002-05", false); !harness.Blocked(r) {
		t.Fatalf("a changed uncommitted set was released by the old count:\n%s", r.Output)
	}

	// Committing ends it: nothing is owed, and the count is gone.
	e.CommitAll(proj, "commit what was owed")
	if r := e.StopJudged(proj, "s-002-05", false); harness.Blocked(r) {
		t.Fatalf("a clean tree was refused:\n%s", r.Output)
	}
	e.WriteFile(proj, "docs/c.md", "c\n")
	if r := e.StopNow(proj, "s-002-05", false); !harness.Blocked(r) {
		t.Fatalf("a new uncommitted set after a clean Stop was not refused:\n%s", r.Output)
	}
}
