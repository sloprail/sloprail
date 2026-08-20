package e2e

import (
	"strings"
	"testing"
)

// coverageRefusal is the gate's own wording for an uncovered scanner.
const coverageRefusal = "no single gh call covering all their keywords"

// T038_04: a declared ACTIVE scanner whose keywords were NEVER searched is
// REFUSED at Stop, and the gate's own reason (naming the scanner) reaches the
// agent.
//
// The core violation the unit exists to catch: a scanner declared but not
// actually searched. The context logs the scanner's keyword set; the gate reads it
// via --owner, finds no gh call in the trajectory covering all its keywords, and
// refuses.
func TestT038_04_UncoveredScannerRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-038-04"
	// Declare an active scanner; run NO gh search — a real coverage violation.
	res := e.Run(proj, sess, "declare a scanner but never search", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
	))

	// The scanner WAS declared (logged) — so this is a genuine uncovered-scanner
	// setup, not an empty turn.
	if _, ok := e.GuardrailState(proj, sess, "scanner-declared", "")["scanner:mine"]; !ok {
		t.Fatalf("precondition: the scanner was not logged, so this is not a real coverage-violation setup")
	}

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("the coverage gate did not refuse an uncovered scanner:\n%s", res.Output)
	}
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, coverageRefusal) {
		t.Errorf("the coverage gate's reason did not reach the agent:\n%s", joined)
	}
	// The uncovered scanner is named, so the agent knows what to search.
	if !strings.Contains(joined, "mine") {
		t.Errorf("the refusal did not name the uncovered scanner:\n%s", joined)
	}
	if !strings.Contains(joined, "verify-scanner-coverage") {
		t.Errorf("the refusal did not name the gate:\n%s", joined)
	}
}

// T038_06: a declared scanner whose keywords ALL appear together in ONE gh call
// ADMITS — the control that proves T038_04's refusal is conditional and that the
// coverage machinery (gh-call extraction + all-keywords-in-one-call check) works.
//
// The agent declares the scanner and runs a single gh search carrying every one of
// its keywords. The gate reads the declared set via --owner, scans the trajectory's
// gh invocations, finds one call covering all keywords, and admits.
func TestT038_06_CoveredScannerAdmits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-038-06"
	// activeScanner declares keywords guardrail, llm, agent — cover them all in one
	// gh call.
	res := e.Run(proj, sess, "declare and search", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
		Bash("b1", "gh search repos guardrail llm agent --limit=10"),
	))

	if _, ok := e.GuardrailState(proj, sess, "scanner-declared", "")["scanner:mine"]; !ok {
		t.Fatalf("precondition: the scanner was not logged")
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("the gate refused a scanner whose keywords were all searched in one call:\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("unexpected refusal:\n%s", res.Output)
	}
}

// T038_07: keywords split ACROSS two gh calls do NOT satisfy coverage — the
// "all keywords in ONE call" rule.
//
// The unit's crux (its own words: "ensuring that gh call has all keywords in 1
// call"). The scanner declares guardrail+llm+agent; the agent searches guardrail
// in one call and llm+agent in another. No SINGLE call carries all three, so the
// gate refuses — proving coverage is checked call-by-call, not as a union across
// calls.
func TestT038_07_KeywordsSplitAcrossCallsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-038-07"
	res := e.Run(proj, sess, "declare and split-search", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
		Bash("b1", "gh search repos guardrail --limit=5"),
		Bash("b2", "gh search repos llm agent --limit=5"),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("keywords split across two gh calls were accepted — coverage must be all-in-ONE-call:\n%s", res.Output)
	}
	if !strings.Contains(strings.Join(blocks, "\n"), coverageRefusal) {
		t.Errorf("refused, but not with the coverage reason:\n%v", blocks)
	}
}

// T038_05: a turn that declares NO scanner is not subject to the coverage gate at
// all — the gate's `match: context["scanner-declared"].active` skips it.
//
// The control that proves the gate is properly scoped: with no scanner declared
// the context is inactive, the gate's `match` is false, the check never runs, and
// nothing is refused. This is the "not active ⇒ not required" declarative path.
func TestT038_05_NoScannerNoGate(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-038-05"
	res := e.Run(proj, sess, "do ordinary work, no scanner", Turns("done",
		Write("w1", "notes/idea.md", "nothing to do with scanners"),
	))

	if active, _ := e.ContextState(proj, sess, "scanner-declared"); active {
		t.Errorf("the context activated with no scanner declared")
	}
	if res.Refused() || len(e.BlockingErrorsFrom(proj, sess, "Stop")) != 0 {
		t.Errorf("the coverage gate refused a turn that declared no scanner:\n%s", res.Output)
	}
}
