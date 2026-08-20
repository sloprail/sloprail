package e2e

import (
	"testing"
)

// T038_04: a declared ACTIVE scanner whose keywords were NEVER searched is NOT
// refused — the coverage enforcement is a silent no-op because the gate cannot
// read the declared-scanner registry.
//
// This PINS the blocking example bug. A scanner is declared active (its keyword
// set logged, asserted below so this is a real violation), and NO gh call is made
// — a clear coverage shortfall. But the gate's check runs `sr-session state list
// --owner scanner-declared`; `--owner` is rejected, the read is empty,
// declared_count is 0, and the gate's backstop `exit 0` passes. So the Stop is
// admitted despite the uncovered scanner.
//
// The test asserts the CURRENT (broken) outcome: no refusal. If the example is
// fixed to read the context payload, this uncovered scanner would be refused with
// "These declared scanners have no single gh call covering all their keywords" and
// THIS test must flip to assert that.
func TestT038_04_UncoveredScannerNotRefused_ExampleBug(t *testing.T) {
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

	// CURRENT behavior: no refusal (the gate read an empty registry via `--owner`).
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) != 0 {
		t.Fatalf("the coverage gate REFUSED an uncovered scanner — the enforcement may have been "+
			"fixed (gate now reads the context payload). Update this test to assert the refusal.\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("unexpected refusal:\n%s", res.Output)
	}
}

// T038_05: a turn that declares NO scanner is not subject to the coverage gate at
// all — the gate's `match: context["scanner-declared"].active` skips it.
//
// The control that proves the gate is properly scoped (unlike interlinking's,
// which lacks a match and blocks unrelated turns): with no scanner declared the
// context is inactive, the gate's `match` is false, the check never runs, and
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
