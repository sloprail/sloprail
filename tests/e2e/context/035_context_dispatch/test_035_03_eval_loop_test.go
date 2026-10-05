package e2e

import (
	"testing"
)

// This file covers the eval-loop-maxing COMPOSITE end to end — the general
// goal-pairing every goal needs (decision 20260818_no-slop-primitives, slice 6):
//
//   - a goal-tracking CONTEXT enters on a PostFileWrite of goal/**/goal.yaml,
//     reads the goal's `enabled` flag off the SETTLED content, and (if enabled)
//     activates carrying the goal's name in its payload;
//   - a goal-verify GATE bound to Stop, require:[{context: goal-tracking}], reads
//     that payload's goal name and runs the goal's verify.sh — BLOCKING the Stop
//     until the target is met ("don't stop until X");
//   - the context's exit reads the gate's verdict from `gates` (top level, per the
//     spec's parity fix — a sibling of `event`, not nested) and deactivates once
//     the gate passed.
//
// This is the whole reversal in one flow: the CONTEXT tracks, the GATE blocks. The
// goal.yaml is written BY THE AGENT mid-trajectory (before that write the goal
// folder does not exist), which is exactly why the context binds Post (the
// `enabled` flag only exists once the write settled).

// goalTrackingContext is the composite's context: enters on a settled goal.yaml
// write, activating only for an enabled goal, carrying the goal's name.
const goalTrackingContext = `on:
  - event: PostFileWrite
    match: event.path startsWith "goal/" and event.path endsWith "goal.yaml"
enter: ./enter.sh
exit: ./exit.sh
`

// goalEnter reads the settled goal.yaml content off event.newContent, activates
// only when enabled:true, and carries the goal's folder name in the payload.
const goalEnter = `#!/bin/sh
input="$(cat)"
content="$(printf '%s' "$input" | sed -n 's/.*"newContent":"\([^"]*\)".*/\1/p' | head -1)"
path="$(printf '%s' "$input" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p' | head -1)"
# The content has escaped newlines (\n) in the JSON; check for enabled:true.
case "$content" in
  *enabled:\ true*|*"enabled: true"*) : ;;
  *) exit 0 ;;   # written but not enabled — do not activate
esac
# goal name is the folder: goal/<name>/goal.yaml
name="$(printf '%s' "$path" | sed -n 's#goal/\([^/]*\)/goal.yaml#\1#p')"
printf '{"goal":"%s"}' "$name"
exit 0
`

// goalExit reads the paired gate's verdict from `gates` (TOP LEVEL — the spec's
// parity: gates is a sibling of event, not nested) and deactivates once the gate
// passed; otherwise stays active.
const goalExit = `#!/bin/sh
input="$(cat)"
status="$(printf '%s' "$input" | sed -n 's/.*"goal-verify":{"status":"\([^"]*\)".*/\1/p' | head -1)"
if [ "$status" = "pass" ]; then
  exit 0   # target met — deactivate
fi
exit 1     # not met — stay active
`

// goalVerifyGate is the composite's gate: bound to Stop, requires the tracking
// context active, and runs the goal's verify.sh — blocking the Stop until it
// passes.
const goalVerifyGate = `on:
  - event: Stop
require:
  - context: goal-tracking
checks:
  - script: ./run-verify.sh
`

// goalRunVerify reads the active goal's name off the GateCheckPayload's top-level
// `context` and runs the goal's verify.sh, blocking the Stop when it fails.
const goalRunVerify = `#!/bin/sh
input="$(cat)"
name="$(printf '%s' "$input" | sed -n 's/.*"goal-tracking":{"active":[^,]*,"payload":{"goal":"\([^"]*\)".*/\1/p' | head -1)"
if [ -z "$name" ]; then
  exit 0   # context not active — nothing to verify
fi
verify="$SR_WORKSPACE/goal/$name/verify.sh"
if [ ! -x "$verify" ]; then
  echo "goal $name active but verify.sh missing at $verify" >&2
  exit 1
fi
if "$verify"; then
  exit 0
fi
echo '{"reason":"the goal target is not yet met — do not stop, keep iterating until verify.sh passes"}'
exit 1
`

// T035_07: the eval-loop-maxing composite blocks the Stop while the goal is unmet
// and admits it once met.
//
// The agent writes goal/accuracy-target/goal.yaml (enabled) and a metrics file
// verify.sh reads. Cycle 1: the metric is BELOW target — the gate blocks the Stop
// ("keep iterating"). Cycle 2: the agent improves the metric to meet the target —
// the gate passes, the Stop is admitted, and the context deactivates.
func TestT035_07_EvalLoopMaxingComposite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "goal-tracking", goalTrackingContext, map[string]string{
		"enter.sh": goalEnter,
		"exit.sh":  goalExit,
	})
	e.Gate(proj, "goal-verify", goalVerifyGate, map[string]string{"run-verify.sh": goalRunVerify})

	// The goal's verify.sh: passes only when metrics.txt holds accuracy>=0.95. It
	// is committed as part of the project (the agent authors goal.yaml during the
	// run; the verify script is the goal's fixed condition).
	verify := `#!/bin/sh
acc="$(cat "$SR_WORKSPACE/metrics.txt" 2>/dev/null || echo 0)"
awk -v a="$acc" 'BEGIN{exit !(a>=0.95)}'
`
	e.WriteExecutable(proj, "goal/accuracy-target/verify.sh", verify)

	sess := "s-035-07"

	// Cycle 1: declare the goal (enabled) and write a BELOW-target metric.
	e.Run(proj, sess, "commit to the accuracy target", Turns("done",
		Write("g1", "goal/accuracy-target/goal.yaml", "enabled: true\nscript: verify.sh"),
		Write("m1", "metrics.txt", "0.80"),
	))

	// The context entered (goal enabled), and the gate blocked the Stop (metric
	// below target).
	active, payload := e.ContextState(proj, sess, "goal-tracking")
	if !active {
		t.Fatalf("the goal-tracking context did not enter on the enabled goal.yaml write")
	}
	if payload["goal"] != "accuracy-target" {
		t.Errorf("the context payload did not carry the goal name: %v", payload)
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("the goal-verify gate did not block the Stop while the target was unmet")
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "keep iterating") {
		t.Errorf("the gate's 'keep iterating' reason did not reach the agent:\n%s", joined)
	}

	// Cycle 2: improve the metric to MEET the target. The gate now passes, the Stop
	// is admitted, and the context deactivates (its exit read the gate's pass).
	//
	// The verdict is read from the gates[] map (the LAST verdict) and the context's
	// active flag, NOT from BlockingErrorsFrom — which accumulates every block ever
	// recorded on the session's transcript, so cycle 1's block is still in it and
	// would falsely read as a cycle-2 block. GateState is the authoritative "what
	// did the gate decide THIS cycle".
	e.Run(proj, sess, "improve the metric to hit the target", Turns("done",
		Write("m2", "metrics.txt", "0.96"),
	))
	if status := e.GateState(proj, sess, "goal-verify"); status != "pass" {
		t.Errorf("the goal-verify gate recorded %q after the target was met, want pass — "+
			"the gate should read the settled metric and admit the Stop", status)
	}
	// The context deactivated once the gate passed (its exit read gates[].status).
	if active, _ := e.ContextState(proj, sess, "goal-tracking"); active {
		t.Errorf("the goal-tracking context stayed active after the goal was met — its exit should read the gate's pass and deactivate")
	}
}
