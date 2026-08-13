package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// A guardrail whose hook LAUNCHES AN AGENT to judge the write, in the very
// project it is guarding.
//
// This is the docs use-case as the owner described it, and it is the shape the
// whole recursion problem lives in: the hook binds PreFileCreate, the agent it
// launches works in the same tree, and the agent's own first Write fires
// PreFileCreate again.
const judgeByAgent = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "notes/"
      hooks:
        - type: command
          command: ./judge.sh
---

# Asks an agent whether the note is any good

The hook runs sr-agent. The agent it launches edits files, because a judging
agent that could not edit files could not fix what it judged.
`

// judgeScript runs an agent and records the depth it was invoked at.
//
// The depth is CARRIED, not inferred from the number of lines: each invocation
// reads the depth it was handed, writes that number down, and hands its child
// one more. So the ledger shows actual nesting rather than a count that a
// fan-out would inflate.
//
// The `-ge 8` stop is the counter the investigation had to build in, and it is
// deliberately kept: without a product-side guard it is the ONLY thing that
// ends the recursion, and a test that removed it would be a fork bomb rather
// than a measurement. Its presence is what makes the depth assertion below
// meaningful — a guard that works means the counter is never reached.
const judgeScript = `#!/bin/sh
cat >/dev/null

D="${SLOP_TEST_DEPTH:-0}"
echo "$D" >> ledger.txt

if [ "$D" -ge 8 ]; then
  echo "test-counter-tripped" >> ledger.txt
  exit 0
fi

SLOP_TEST_DEPTH=$((D + 1))
export SLOP_TEST_DEPTH

sr-agent --harness claude-code --model size-xs "judge this note" >/dev/null 2>&1
exit 0
`

// maxDepth is the deepest level the ledger recorded.
func maxDepth(t *testing.T, lines []string) int {
	t.Helper()
	deepest := -1
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || l == "test-counter-tripped" {
			continue
		}
		n, err := strconv.Atoi(l)
		if err != nil {
			continue
		}
		if n > deepest {
			deepest = n
		}
	}
	return deepest
}

// T015_01: a hook that launches an agent runs ONCE, not once per level.
//
// The measurement this pins was taken with the guard removed: the ledger read
// 0,1,2,3,4,5,6,7,8 and then the test's own counter, exactly the depth-8
// runaway the investigation reported. With the guard the hook runs at depth 0
// and the agent it launches is not judged by the rule that launched it.
//
// Nothing here calls sloprail directly. The agent writes, the harness fires
// PreToolUse, the plugin reaches our subcommand, the hook runs sr-agent, and
// sr-agent execs a `claude` that the harness has shimmed to the mock — the same
// wiring a user installing this would get, with only the harness binary
// substituted.
func TestT015_01_LaunchedAgentDoesNotReenterTheRuleThatLaunchedIt(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "judge-notes", judgeByAgent, map[string]string{"judge.sh": judgeScript})
	e.InstallClaudeShim(proj)
	// The launched agent EDITS A FILE. That is the case worth protecting: a
	// judging agent that could not write could not be the thing this guards.
	e.InnerScenario(proj, Turns("judged", Write("i1", "notes/judged.md", "ok")))

	e.Run(proj, "s-015-01", "write a note", Turns("done",
		Write("w1", "notes/first.md", "hello"),
	))

	ledger := e.Ledger(proj, "judge-notes", "ledger.txt")
	t.Logf("ledger: %v", ledger)

	for _, l := range ledger {
		if strings.TrimSpace(l) == "test-counter-tripped" {
			t.Fatalf("the recursion ran away and was stopped only by the test's own counter; ledger: %v", ledger)
		}
	}

	if got := maxDepth(t, ledger); got != 0 {
		t.Fatalf("the rule that launched the agent re-entered itself: deepest level %d, want 0; ledger: %v", got, ledger)
	}
}
