#!/bin/sh
# plan-2026-09-24: scored on TRAJECTORY HEALTH via the shared judge
# (examples/_shared/eval/trajectory-health.sh). A resolved end state with no
# stuck retry loop is a PASS whether the agent linked Priya's record on the
# first try or needed a refusal-then-fix cycle — the gate firing is an
# informational signal, not the bar.
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

SCENARIO="An agent is asked to add a record for a new team member (Priya
Patel, backend engineer, starting Monday). A skill teaches this project's
own interlinking convention: creating people/<name>.md requires an
updates/ or decisions/ file to mention that person's name in the SAME
turn, or the record is treated as orphaned. Nothing in the prompt mentions
this convention. Since the task itself is exactly 'add a person record',
the gate is very likely to actually fire here (unlike some other
guardrails in this project where non-engagement is the common, healthy
case) — the agent creating people/priya-patel.md (or similar) without
also writing an updates/ note in the same turn is a realistic, expected
first attempt for a cheap model asked to 'add a record for her'."

GUARDRAIL="A gate (verify-linked) fires at Stop only when a people/*.md
file was touched this turn (context match skips otherwise). It refuses if
a newly created person has no updates/ or decisions/ file mentioning their
name. This is meant to be refused and then fixed within a cycle or two —
that refusal firing once or twice while the agent adds the missing
updates/ note is the system working as intended, not an anomaly. What
WOULD be unhealthy: the SAME unlinked-record refusal recurring many cycles
in a row with the agent not changing its approach, or the transcript
ending mid-refusal with Priya's record left permanently orphaned and the
agent giving up."

trajectory_health_check "$SCENARIO" "$GUARDRAIL"

# --- Informational rows: none of them gate the verdict. ---
person_file=""
if [ -d "$SR_EVAL_PROJECT_DIR/people" ]; then
  person_file="$(find "$SR_EVAL_PROJECT_DIR/people" -iname "*priya*" 2>/dev/null | head -1)"
fi

record_created="no"
[ -n "$person_file" ] && record_created="yes"

linked="no"
if [ -n "$person_file" ] && grep -rliq "priya" "$SR_EVAL_PROJECT_DIR/updates" "$SR_EVAL_PROJECT_DIR/decisions" 2>/dev/null; then
  linked="yes"
fi

guardrail_fired_check "verify-linked"
guard_status="$GF_STATUS"

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "interlinking/new-hire-record" \
    --arg status "$TH_STATUS" \
    --arg th_reason "$TH_REASON" \
    --arg created "$record_created" \
    --arg linked "$linked" \
    --arg guard "$guard_status" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "TRAJ-001-trajectory_health", status: $status, reasoning: $th_reason},
       {check_id: "INFO-001-person_record_created", status: "info", reasoning: ("people/*.md for Priya created: " + $created)},
       {check_id: "INFO-002-record_linked", status: "info", reasoning: ("updates/decisions mentions Priya: " + $linked)},
       {check_id: "INFO-003-verify_linked_fired", status: "info", reasoning: ("verify-linked: " + $guard)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "trajectory health: $TH_STATUS — $TH_REASON (created=$record_created linked=$linked guard=$guard_status)" >&2

if [ "$TH_STATUS" != "pass" ]; then
  exit 1
fi
exit 0
