package e2e

import (
	"strings"
	"testing"
)

// This file drives the depth gate's PASS path and the page-count check, both now
// reachable since the clone is detected from the `git clone` INVOCATION (rather
// than an unreachable `.toolUseResult`).

// T039_04: a research run that DID `git clone` AND ran `gh` with enough page
// coverage ADMITS.
//
// The happy path, and the control that proves the depth refusals (T039_01, and
// T039_05/06 below) are conditional. The clone is detected from its invocation;
// the gh call's `--paginate` (unbounded) satisfies the page minimum; the depth
// gate passes and the Stop is admitted.
func TestT039_04_DeepResearchAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-039-04"
	res := e.Run(proj, sess, "deep research", Turns("done",
		// #research declared atomically with a real git clone invocation.
		SayBash("b1", "Cloning to study it. #research", "git clone https://github.com/owner/repo /tmp/study"),
		// A gh search whose --paginate covers unbounded pages (>= the minimum).
		Bash("b2", "gh search repos guardrail llm agent --paginate"),
	))

	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("a deep research run (clone + paginated gh) was refused:\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("unexpected refusal:\n%s", res.Output)
	}
	// The gate recorded a pass this cycle — the authoritative "the depth check
	// admitted". (The context, whose exit reads that pass, then deactivates: the
	// same reversal the goal composite uses, so the context is not asserted active
	// here.)
	if st := e.GateState(proj, sess, "depth-check"); st != "pass" {
		t.Errorf("the depth gate recorded %q, want pass", st)
	}
	// And the context DID enter and run its checks — it activated at Stop before
	// the gate, then deactivated once the gate passed.
	if active, _ := e.ContextState(proj, sess, "research-run"); active {
		t.Errorf("the research context stayed active after the depth gate passed — its exit should deactivate it")
	}
}

// T039_05: a research run that cloned but ran NO gh call is REFUSED on the
// page-count check — a distinct violation path from the missing clone.
//
// Check 1 (clone) passes; check 2 (gh calls) finds none and refuses "No gh CLI
// calls found". Proves the depth checks run in sequence and each is enforced.
func TestT039_05_CloneButNoSearchRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-039-05"
	res := e.Run(proj, sess, "clone but do not search", Turns("done",
		SayBash("b1", "Cloning. #research", "git clone https://github.com/owner/repo /tmp/study"),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a research run with no gh search was not refused:\n%s", res.Output)
	}
	if !strings.Contains(strings.Join(blocks, "\n"), "No gh CLI calls found") {
		t.Errorf("refused, but not with the no-gh-calls reason:\n%v", blocks)
	}
}

// T039_06: a research run that cloned and searched but covered TOO FEW pages is
// REFUSED, and the refusal reports the shortfall.
//
// Check 1 and the gh-present part of check 2 pass; the page count (a `--limit 2`
// call = 2 pages) falls below the minimum of 5, so the gate refuses with the
// actual page count. Proves the page-count arithmetic works (including reading the
// space-separated --limit value out of argv).
func TestT039_06_TooFewPagesRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-039-06"
	res := e.Run(proj, sess, "shallow paging", Turns("done",
		SayBash("b1", "Cloning. #research", "git clone https://github.com/owner/repo /tmp/study"),
		Bash("b2", "gh search repos guardrail llm agent --limit 2"),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a research run covering only 2 pages was not refused:\n%s", res.Output)
	}
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, "only 2 page(s)") || !strings.Contains(joined, "below the minimum of 5") {
		t.Errorf("refused, but the reason did not report the page shortfall:\n%s", joined)
	}
}
