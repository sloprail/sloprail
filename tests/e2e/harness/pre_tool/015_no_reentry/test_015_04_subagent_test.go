package e2e

import (
	"strings"
	"testing"
)

// A SECOND launching rule, bound to a different path, whose check also launches
// an agent.
//
// Two launching rules is what makes the sub-agent case reachable: the agent
// judge-notes launches writes under review/, which is where THIS rule is bound,
// so its check fires inside the launched agent and launches one of its own.
const reviewByAgent = `on:
  - event: PreFileWrite
    match: event.path startsWith "review/"
checks:
  - script: ./review.sh
`

// Each launching check records its own depth, so the ledger distinguishes "ran
// once at the level it should" from "ran again one level down".
const depthScript = `#!/bin/sh
cat >/dev/null
D="${SLOP_TEST_DEPTH:-0}"
echo "$D" >> ledger.txt
if [ "$D" -ge 6 ]; then
  echo "test-counter-tripped" >> ledger.txt
  exit 0
fi
SLOP_TEST_DEPTH=$((D + 1))
export SLOP_TEST_DEPTH
sr-agent --harness claude-code --model size-xs "judge" >/dev/null 2>&1
exit 0
`

// T015_04: a launched agent may itself launch one, and NEITHER rule re-enters.
//
// The nesting the brief requires the guard to survive. The outer session writes
// under notes/, so judge-notes launches an agent; that agent writes under
// review/, so review-docs launches one; and that third-level agent writes under
// notes/ again — which is where a guard that remembered only the NEAREST
// launching rule would let judge-notes fire a second time.
//
// # What is asserted, and what is NOT
//
// Each launching rule runs EXACTLY ONCE across the whole chain. That is the
// invariant; "at depth 0" is not, and asserting it was wrong the first time
// this test was written. review-docs legitimately fires at depth 1: it is a
// DIFFERENT rule from the one that launched that session, so the design says it
// must still be enforced there — the same property T015_02 pins. A guard that
// silenced it would be the scope answer, which this branch rejects.
//
// So the chain terminates because each rule is spent once, not because nesting
// is forbidden. Measured with the fix removed, judge-notes' ledger reads
// 0, 6, test-counter-tripped — it re-enters and runs away.
//
// A single-valued marker passes T015_01 and fails here — which is exactly why
// the engine appends (appendLaunchedBy) rather than replacing: the chain carries
// every launcher above it, not merely the nearest.
// sr:proves checks/check-launched-agent-does-not-reenter-its-rule
func TestT015_04_SubagentDoesNotReenterEitherRule(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "judge-notes", judgeByAgent, map[string]string{"judge.sh": depthScript})
	e.Gate(proj, "review-docs", reviewByAgent, map[string]string{"review.sh": depthScript})
	e.InstallClaudeShim(proj)
	// Every launched agent runs this same scenario: it writes under review/
	// (reaching review-docs) and then under notes/ (reaching judge-notes again).
	// So if either rule is enforceable inside a session it launched, the ledger
	// shows a level greater than zero.
	e.InnerScenario(proj, Turns("judged",
		Write("i1", "review/pass.md", "reviewed"),
		Write("i2", "notes/again.md", "noted"),
	))

	e.Run(proj, "s-015-04", "write a note", Turns("done",
		Write("w1", "notes/first.md", "hello"),
	))

	for _, rule := range []string{"judge-notes", "review-docs"} {
		ledger := gateLedgerLines(t, proj, rule, "ledger.txt")
		t.Logf("%s ledger: %v", rule, ledger)

		for _, l := range ledger {
			if strings.TrimSpace(l) == "test-counter-tripped" {
				t.Fatalf("%s ran away and was stopped only by the test's own counter: %v", rule, ledger)
			}
		}
		// Once, not once per level. A rule that appeared twice re-entered
		// itself through the sub-agent, which is the bug displaced one turn.
		if len(ledger) != 1 {
			t.Fatalf("%s ran %d times, want exactly 1 — it re-entered itself through a sub-agent: %v",
				rule, len(ledger), ledger)
		}
	}
}
