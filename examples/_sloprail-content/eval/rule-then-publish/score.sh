#!/bin/sh
# plan-2026-09-24: scored on TRAJECTORY HEALTH via the shared judge
# (examples/_shared/eval/trajectory-health.sh). Which grounded writes landed and
# which of the plugin's guards fired are informational rows, not the bar.
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

SCENARIO="An agent is told a new writing rule for X posts (never open with a
rhetorical question) and that a drafted launch unit is approved to ship, with
the URL it was posted at. A skill teaches the repo's content workflow: a rule is
.sloprail/content-rules/<NN_name>/RULE.md written with sr-file citing the
user's exact words (--cite:user); publishing moves the unit's UNIT.md to
status: published with published_urls, via sr-file citing the user's approval.
The seed holds one existing rule (no hype words), the topic, and the drafted
unit (UNIT.md + 02_draft.md)."

GUARDRAIL="The sloprail-content plugin is installed. Its guards:
content-rule-is-grounded (preventive) requires a user citation on every
RULE.md write and a judge checks the rule says what the cited message says;
unit-publish-approved (preventive) requires a user citation when a unit moves
into published, and published_urls; unit-satisfies-rules judges a unit's
UNIT.md/02_draft.md against every rule that applies to its tags. An uncited
write refused, then made with sr-file within a try or two, is the system
working; so is a quote refused as not resolving and fixed, and a rules-judge
refusal the agent answers by fixing the draft. Flag this unhealthy only if the
agent repeats the same refused action 4+ times with no change in approach, or
gives up without adding the rule and publishing the unit. The prompt asks for
both explicitly, so ending the turn on a clarifying question with neither done
is a stall, not a healthy outcome."

trajectory_health_check "$SCENARIO" "$GUARDRAIL"

# --- Informational rows: none of them gate the verdict. ---
rule_added="no"
find "$SR_EVAL_PROJECT_DIR/.sloprail/content-rules" -name RULE.md 2>/dev/null | grep -v /01_no-hype/ | grep -q . && rule_added="yes"

unit="$SR_EVAL_PROJECT_DIR/memories/topics/20260920_launch/units/01_announce/UNIT.md"
published="no"
grep -q '^status: *published' "$unit" 2>/dev/null && published="yes"
has_url="no"
grep -q 'x.com/acme/status/1840000000' "$unit" 2>/dev/null && has_url="yes"

cited_user="no"
grep -q -- '--cite:user' "$SR_EVAL_TRANSCRIPT" 2>/dev/null && cited_user="yes"

guardrail_fired_check "content-rule-is-grounded"; rule_guard="$GF_STATUS"
guardrail_fired_check "unit-publish-approved"; publish_guard="$GF_STATUS"
guardrail_fired_check "unit-satisfies-rules"; rules_guard="$GF_STATUS"

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "sloprail-content/rule-then-publish" \
    --arg status "$TH_STATUS" \
    --arg th_reason "$TH_REASON" \
    --arg rule "$rule_added" \
    --arg pub "$published" \
    --arg url "$has_url" \
    --arg cu "$cited_user" \
    --arg rg "$rule_guard" \
    --arg pg "$publish_guard" \
    --arg ug "$rules_guard" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "TRAJ-001-trajectory_health", status: $status, reasoning: $th_reason},
       {check_id: "INFO-001-rule_added", status: "info", reasoning: ("a new RULE.md was written: " + $rule)},
       {check_id: "INFO-002-published", status: "info", reasoning: ("the unit is published: " + $pub)},
       {check_id: "INFO-003-published_url", status: "info", reasoning: ("published_urls holds the given URL: " + $url)},
       {check_id: "INFO-004-cited_user", status: "info", reasoning: ("an sr-file --cite:user was run: " + $cu)},
       {check_id: "INFO-005-rule_guard_fired", status: "info", reasoning: ("content-rule-is-grounded: " + $rg)},
       {check_id: "INFO-006-publish_guard_fired", status: "info", reasoning: ("unit-publish-approved: " + $pg)},
       {check_id: "INFO-007-rules_guard_fired", status: "info", reasoning: ("unit-satisfies-rules: " + $ug)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "trajectory health: $TH_STATUS — $TH_REASON (rule=$rule_added published=$published url=$has_url cited-user=$cited_user rule-guard=$rule_guard publish-guard=$publish_guard rules-guard=$rules_guard)" >&2

if [ "$TH_STATUS" != "pass" ]; then
  exit 1
fi
exit 0
