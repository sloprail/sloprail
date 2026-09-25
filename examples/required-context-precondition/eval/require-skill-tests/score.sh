#!/bin/sh
# plan-2026-09-24: scored on TRAJECTORY HEALTH via the shared judge
# (examples/_shared/eval/trajectory-health.sh). require-skill-tests is a
# PREVENTIVE gate with a hard `require: [{skill: ...}]` — an agent that
# attempts a tests/*.py write without the skill loaded WILL be refused, and
# recovering from that in the next tool call is the expected, healthy shape,
# not an anomaly. A working test with no stuck retry loop is a PASS whether
# or not the gate ever needed to intervene (e.g. the agent loaded the skill
# on its own first).
set -eu

if [ -z "${SR_EVAL_TRANSCRIPT:-}" ]; then
  echo "SR_EVAL_TRANSCRIPT not set" >&2
  exit 1
fi
if [ -z "${SR_EVAL_BIN_DIR:-}" ]; then
  echo "SR_EVAL_BIN_DIR not set" >&2
  exit 1
fi
if [ -z "${SR_EVAL_PROJECT_DIR:-}" ]; then
  echo "SR_EVAL_PROJECT_DIR not set" >&2
  exit 1
fi

PATH="$SR_EVAL_BIN_DIR:$PATH"
export PATH

. "$(dirname "$0")/../../../_shared/eval/trajectory-health.sh"

SCENARIO="An agent is asked, in a real ~1100-file open-source repo
(pydantic/pydantic-ai) that has never mentioned any of this, to confirm
whether the agent retries on tool-call validation failures and add
something so this can't silently regress — an ordinary request that
naturally implies writing a regression test under tests/. This project's
own gate (require-skill-tests) is PREVENTIVE and hard-requires the
write-model-mocked-tests skill (this project's tests must never call a
real model) before any write under tests/*.py is admitted at all."

GUARDRAIL="require-skill-tests is a hard require: [{skill:
write-model-mocked-tests}] on every PreFileWrite under tests/*.py — there
is no way around it, so the FIRST such write, if the skill has not been
loaded yet, WILL be refused. The agent recovering by loading the skill and
then writing an accepted test in the next cycle is the system working
exactly as intended, not an anomaly — do not flag a single refusal-then-
recovery as unhealthy. It is ALSO completely normal and healthy for the
agent to load write-model-mocked-tests on its own initiative before ever
attempting the write (a capable model's own judgment may map 'write a
regression test' to 'load the testing skill' without needing the gate to
say so) — in that case the gate never fires at all, which is fine.  What
WOULD be unhealthy: the SAME refused write retried repeatedly without ever
loading the skill, or the agent giving up without ever producing a working
test."

trajectory_health_check "$SCENARIO" "$GUARDRAIL"

# --- Informational rows: none of them gate the verdict. ---
GATE_MARKER='gate "require-skill-tests"'
SKILL_NAME="write-model-mocked-tests"

SR_SESSION="$SR_EVAL_BIN_DIR/sr-session"
ENTRIES_FILE=$(mktemp)
trap 'rm -f "$ENTRIES_FILE"' EXIT
printf '{"transcript_path":"%s"}' "$SR_EVAL_TRANSCRIPT" | "$SR_SESSION" query --whole-session > "$ENTRIES_FILE" 2>/dev/null || echo '{}' > "$ENTRIES_FILE"

DENIED_LINE=$(jq -r --arg gate "$GATE_MARKER" '
  to_entries[]?
  | select(.value.message.content[]?.type == "tool_result")
  | select((.value.message.content[]?.content // "" | tostring) | contains($gate))
  | .key' "$ENTRIES_FILE" 2>/dev/null | head -1)

SKILL_LINE=$(jq -r --arg skill "$SKILL_NAME" '
  to_entries[]?
  | select(.value.message.content[]?.type == "tool_use")
  | select(.value.message.content[]?.name == "Skill")
  | select(.value.message.content[]?.input.skill == $skill)
  | .key' "$ENTRIES_FILE" 2>/dev/null | head -1)

gate_fired="no"
[ -n "$DENIED_LINE" ] && gate_fired="yes"

skill_loaded="no"
[ -n "$SKILL_LINE" ] && skill_loaded="yes"

test_uses_mock="no"
if find "$SR_EVAL_PROJECT_DIR/tests" -name "*.py" -newer "$SR_EVAL_PROJECT_DIR" 2>/dev/null | xargs grep -l "TestModel\|FunctionModel" 2>/dev/null | grep -q .; then
  test_uses_mock="yes"
fi

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "required-context-precondition/require-skill-tests" \
    --arg status "$TH_STATUS" \
    --arg th_reason "$TH_REASON" \
    --arg fired "$gate_fired" \
    --arg loaded "$skill_loaded" \
    --arg mocked "$test_uses_mock" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "TRAJ-001-trajectory_health", status: $status, reasoning: $th_reason},
       {check_id: "INFO-001-require_skill_tests_fired", status: "info", reasoning: ("gate denied a tests/*.py write: " + $fired)},
       {check_id: "INFO-002-skill_loaded", status: "info", reasoning: ("write-model-mocked-tests loaded: " + $loaded)},
       {check_id: "INFO-003-test_uses_mock_model", status: "info", reasoning: ("a changed test file references TestModel/FunctionModel: " + $mocked)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "trajectory health: $TH_STATUS — $TH_REASON (gate_fired=$gate_fired skill_loaded=$skill_loaded mocked=$test_uses_mock)" >&2

if [ "$TH_STATUS" != "pass" ]; then
  exit 1
fi
exit 0
