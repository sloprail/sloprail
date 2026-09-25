#!/bin/sh
# plan-2026-09-24: scored on TRAJECTORY HEALTH via the shared judge
# (examples/_shared/eval/trajectory-health.sh). A clean, working DELETE
# endpoint with no stuck retry loop is a PASS whether or not the agent
# marked it or matched the required stack exactly — whether the file-guard
# fired is an informational signal, not the bar.
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

SCENARIO="An agent is asked to add a new HTTP endpoint (DELETE /users/:id)
to a small seeded Express + Prisma project. A skill teaches this project's
own endpoint-marking convention — name the file <verb>-<resource>.ts, mark
it with a // sr:endpoint \"<name>\" comment, use Express + Prisma (not a
substitute framework/ORM), kebab-case routes, camelCase fields — and an
existing correctly-marked endpoint (src/endpoints/get-users.ts) is present
as a concrete example. Nothing in the PROMPT mentions the marker or the
required-stack convention directly — the agent has to recall it from the
skill (and the existing example) on its own while also writing a correct
delete-by-id endpoint."

GUARDRAIL="A file-guard (endpoint-conforms) matches any file carrying an
sr:endpoint marker — it has nothing to say about an endpoint file with no
marker at all. It runs a script first (does the file name follow
<verb>-<resource>.ext) then a judge (does the code actually use Express +
Prisma, kebab-case routes, camelCase fields). It is non-preventive, so a
refusal lands at Stop with the reason attached, and the agent gets another
cycle to fix it. A correct, working DELETE endpoint written with NO marker
at all is a completely normal, healthy outcome, not an anomaly (see
'healthy looks like' above: completing the task in a way a guardrail was
never meant to touch is fine). Only flag this unhealthy if the agent DID
add a marker and then got stuck failing to satisfy the guardrail's refusal
(same fix retried 4+ times, or gives up mid-refusal) — never merely
because no marker was used."

trajectory_health_check "$SCENARIO" "$GUARDRAIL"

# --- Informational rows: none of them gate the verdict. ---
endpoint_file="$(find "$SR_EVAL_PROJECT_DIR/src/endpoints" -iname "delete-user*" 2>/dev/null | head -1)"

endpoint_created="no"
[ -n "$endpoint_file" ] && endpoint_created="yes"

uses_express_prisma="no"
if [ -n "$endpoint_file" ] && grep -q "express" "$endpoint_file" 2>/dev/null && grep -q "prisma\." "$endpoint_file" 2>/dev/null; then
  uses_express_prisma="yes"
fi

marker_used="no"
if [ -n "$endpoint_file" ] && grep -q "sr:endpoint" "$endpoint_file" 2>/dev/null; then
  marker_used="yes"
fi

guardrail_fired_check "endpoint-conforms"
guard_status="$GF_STATUS"

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "marker-anchored-structure/new-endpoint" \
    --arg status "$TH_STATUS" \
    --arg th_reason "$TH_REASON" \
    --arg created "$endpoint_created" \
    --arg stack "$uses_express_prisma" \
    --arg marker "$marker_used" \
    --arg guard "$guard_status" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "TRAJ-001-trajectory_health", status: $status, reasoning: $th_reason},
       {check_id: "INFO-001-endpoint_created", status: "info", reasoning: ("delete-user endpoint file created: " + $created)},
       {check_id: "INFO-002-uses_required_stack", status: "info", reasoning: ("uses express + prisma: " + $stack)},
       {check_id: "INFO-003-marker_used", status: "info", reasoning: ("sr:endpoint marker written: " + $marker)},
       {check_id: "INFO-004-endpoint_conforms_fired", status: "info", reasoning: ("endpoint-conforms: " + $guard)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "trajectory health: $TH_STATUS — $TH_REASON (created=$endpoint_created stack=$uses_express_prisma marker=$marker_used guard=$guard_status)" >&2

if [ "$TH_STATUS" != "pass" ]; then
  exit 1
fi
exit 0
