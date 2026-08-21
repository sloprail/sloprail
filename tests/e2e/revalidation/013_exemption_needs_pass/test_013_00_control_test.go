package e2e

import (
	"strings"
	"testing"
)

// The positive control for every skip test in this tree.
//
// A revalidation test asserts a check did NOT run again. That claim passes
// trivially when the check never ran at all, and it passes just as trivially
// when the check ran every time because the engine could not identify the
// session and so had no record to skip against. Both failures are silent, and
// both make a green suite mean nothing.
//
// The branch that first wrote an e2e for this feature hit exactly that: the
// mock's main-session payload carried no transcript_path, session identity
// failed, no store was ever opened, every check re-judged — and the test passed
// whether the skip worked or not. It deleted the test rather than bank a vacuous
// one.
//
// So this file establishes the thing every other file here depends on, and it
// establishes it POSITIVELY: something is written into the session's state in
// one check invocation and read back in the next. Nothing about that can pass by
// accident. A store that never opened returns an error to the check; a store
// keyed differently on each invocation returns "not found"; only a store that
// really opened, under one identity, across two separate check processes, can
// hand back what the earlier one put there.
//
// # RE-VEHICLED onto the NEW file-guard nature (was old GUARDRAIL.md hooks)
//
// The session store a check reads with `sr-session state` is SHARED machinery: a
// file-guard's after-check runs `sr-session state` the same way an old hook did,
// keyed by the same conversation identity, and reaches its own per-guard scope via
// the same SR_GUARDRAIL env. So the round-trip this control proves is unchanged by
// the vehicle — the control here is a file-guard whose after-check records what it
// read back before writing its own mark. Its ledger moves to
// `.sloprail/file-guard/control/log` (read with e.FileGuardLedgerLines), written
// via $SR_GUARDRAIL_DIR; `match: "**/*.md"` selects the two written files without
// matching the guard's own `log`.
//
// The skip-gate every other test calls, harness.RequireSessionStore, still probes
// the same observable capability through the harness's own control — that is a
// precondition check, not a rule under test, so it is left as it is.

// controlGuard is a file-guard whose after-check reads its own session state back
// and records what it found, then writes its own mark. Reading back is the only
// shape that can tell a working store from a broken one: a check that merely WROTE
// would leave a broken store looking like a working one.
const controlGuard = `match: "**/*.md"
checks:
  - script: ./probe.sh
`

const controlScript = `#!/bin/sh
cat >/dev/null
echo "before=[$(sr-session state get seen 2>&1)]" >> "$SR_GUARDRAIL_DIR/log"
sr-session state set seen yes >/dev/null 2>&1
exit 0
`

// T013_00: state written by one check is read by the next in the same session.
//
// The control itself, stated as a test so its failure is reported rather than only
// causing skips elsewhere. Both halves can genuinely fail: the first invocation must
// find NOTHING (a store handing back a value nobody wrote would be a different bug),
// and the second must find what the first left.
//
// Two different paths in one cycle — so neither invocation can be exempted by the
// other. Both are fine content, so both are judged (a refusal would stop nothing
// here, but the check must run twice for the round-trip to be observable), and
// neither shares the other's subject.
func TestT013_00_ControlSessionStateRoundTrips(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "control", controlGuard, map[string]string{"probe.sh": controlScript})

	e.Run(proj, "s-013-00c", "write twice", Turns("done",
		Write("w1", "one.md", "first"),
		Write("w2", "two.md", "second"),
	))

	lines := e.FileGuardLedgerLines(proj, "control", "log")
	if len(lines) != 2 {
		t.Fatalf("want the check to run once per write, got %d: %v", len(lines), lines)
	}
	if strings.Contains(lines[0], "before=[yes]") {
		t.Fatalf("the first check of the session found state nobody had written: %q", lines[0])
	}
	if !strings.Contains(lines[1], "before=[yes]") {
		t.Fatalf("a check could not read back what the previous check in the same session stored, "+
			"so no skip in this tree can be observed and every test gated on this control is "+
			"vacuous. The pieces this needs — a transcript path on the payload and the hook "+
			"environment `session state` resolves its scope from — are in this tree, so this is a "+
			"regression rather than a missing branch. Got: %v", lines)
	}
}
