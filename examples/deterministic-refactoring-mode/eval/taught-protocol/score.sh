#!/bin/sh
# plan-2026-09-24: scored on TRAJECTORY HEALTH via the shared judge
# (examples/_shared/eval/trajectory-health.sh), not on whether the agent
# specifically followed the taught #refactor/sr:moved-from protocol. A clean,
# working refactor with no stuck retry loop is a PASS whether or not the tag
# convention was used exactly right — the protocol-fidelity signals below are
# recorded as informational rows, not the gate.
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

SCENARIO="An agent is working in a real, ~14k-line open source Python repo
(pallets/click). It is asked to split src/click/exceptions.py's exception
classes into two files by category (usage errors vs. control-flow errors),
updating every import across the codebase so nothing breaks, deleting the
old file. A project skill teaches the agent a #refactor/sr:moved-from
declaration convention this project's tooling checks moves against."

GUARDRAIL="A PreFileWrite gate with a same-named Stop file-guard (moved-content-reconciles)
and a Stop gate (refactor-complete) verify that a move declared via '#refactor scope=...'
and marked with 'sr:moved-from <fqn>' actually carries the origin's exact
bytes rather than regenerated ones, and that every declared move actually
landed. The agent was taught this convention by a skill, so it is expected
to often use it — but using it imperfectly, or completing the task without
it, does not by itself make this an unhealthy trajectory."

trajectory_health_check "$SCENARIO" "$GUARDRAIL"

# --- Informational rows: protocol fidelity, none of them gate the verdict. ---
USAGE_FILE="$SR_EVAL_PROJECT_DIR/src/click/usage_exceptions.py"
CONTROL_FILE="$SR_EVAL_PROJECT_DIR/src/click/control_exceptions.py"

split_done="no"
if [ -f "$USAGE_FILE" ] && [ -f "$CONTROL_FILE" ]; then
  split_done="yes"
fi

tag_seen="no"
if grep -qF '#refactor' "$SR_EVAL_TRANSCRIPT" 2>/dev/null; then
  tag_seen="yes"
fi

markers_landed=0
for f in "$USAGE_FILE" "$CONTROL_FILE"; do
  if [ -f "$f" ] && grep -q 'sr:moved-from' "$f" 2>/dev/null; then
    markers_landed=$((markers_landed + 1))
  fi
done

guardrail_fired_check "moved-content-reconciles"
fg_status="$GF_STATUS"
guardrail_fired_check "refactor-complete"
gate_status="$GF_STATUS"

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "deterministic-refactoring-mode/taught-protocol" \
    --arg status "$TH_STATUS" \
    --arg th_reason "$TH_REASON" \
    --arg split "$split_done" \
    --arg tag "$tag_seen" \
    --arg markers "$markers_landed" \
    --arg fg "$fg_status" \
    --arg gate "$gate_status" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "TRAJ-001-trajectory_health", status: $status, reasoning: $th_reason},
       {check_id: "INFO-001-split_completed", status: "info", reasoning: ("split done: " + $split)},
       {check_id: "INFO-002-refactor_tag_seen", status: "info", reasoning: ("#refactor tag seen: " + $tag)},
       {check_id: "INFO-003-markers_landed", status: "info", reasoning: ("sr:moved-from markers landed: " + $markers + "/2")},
       {check_id: "INFO-004-file_guard_fired", status: "info", reasoning: ("moved-content-reconciles: " + $fg)},
       {check_id: "INFO-005-gate_fired", status: "info", reasoning: ("refactor-complete: " + $gate)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "trajectory health: $TH_STATUS — $TH_REASON (split=$split_done tag=$tag_seen markers=$markers_landed/2 file-guard=$fg_status gate=$gate_status)" >&2

if [ "$TH_STATUS" != "pass" ]; then
  exit 1
fi
exit 0
