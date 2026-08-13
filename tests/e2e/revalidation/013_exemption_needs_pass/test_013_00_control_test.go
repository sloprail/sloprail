package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The positive control for every skip test in this tree.
//
// A revalidation test asserts a hook did NOT run again. That claim passes
// trivially when the hook never ran at all, and it passes just as trivially
// when the hook ran every time because the engine could not identify the
// session and so had no record to skip against. Both failures are silent, and
// both make a green suite mean nothing.
//
// The branch that first wrote an e2e for this feature hit exactly that: the
// mock's main-session PreToolUse payload carries no transcript_path, session
// identity failed, no store was ever opened, every hook re-judged — and the
// test passed whether the skip worked or not. It deleted the test rather than
// bank a vacuous one.
//
// So this file establishes the thing every other file here depends on, and it
// establishes it POSITIVELY: something is written into the session's state in
// one hook invocation and read back in the next. Nothing about that can pass
// by accident. A store that never opened returns an error to the hook; a store
// keyed differently on each invocation returns "not found"; only a store that
// really opened, under one identity, across two separate hook processes, can
// hand back what the earlier one put there.
//
// If T013_00 skips, every skip-dependent test in this tree skips with it, and
// the reason is named. That is deliberate: a suite that cannot establish its
// own control must not report coverage it does not have.

// T013_00: state written by one hook is read by the next in the same session.
//
// The control itself, stated as a test so its failure is reported rather than
// only causing skips elsewhere. Its assertions are the same ones storeOpens
// SessionStoreOpens makes, and both halves can genuinely fail: the first invocation must find
// NOTHING (a store handing back a value nobody wrote would be a different bug),
// and the second must find what the first left.
func TestT013_00_ControlSessionStateRoundTrips(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "control", harness.ControlDecl, map[string]string{"probe.sh": harness.ControlScript})

	// Two different paths — see harness.SessionStoreOpens on why the control must not be
	// skippable by the mechanism it exists to make testable.
	e.Run(proj, "s-013-00c", "write twice", Turns("done",
		Write("w1", "one.md", "first"),
		Write("w2", "two.md", "second"),
	))

	lines := e.Ledger(proj, "control", "log")
	if len(lines) != 2 {
		t.Fatalf("want the hook to run once per write, got %d: %v", len(lines), lines)
	}
	if strings.Contains(lines[0], "before=[yes]") {
		t.Fatalf("the first hook of the session found state nobody had written: %q", lines[0])
	}
	if !strings.Contains(lines[1], "before=[yes]") {
		t.Fatalf("a hook could not read back what the previous hook in the same session stored, "+
			"so no skip in this tree can be observed and every test gated on this control is "+
			"vacuous. The pieces this needs — a transcript path on the PreToolUse payload and "+
			"c.Env on the hook process — are in this tree, so this is a regression rather than "+
			"a missing branch. Got: %v", lines)
	}
}
