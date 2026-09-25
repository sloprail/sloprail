#!/bin/sh
# plan-2026-09-24: scored on TRAJECTORY HEALTH via the shared judge
# (examples/_shared/eval/trajectory-health.sh). An accurate summary with no
# stuck retry loop is a PASS whether or not the agent used citations at all
# — whether the file-guard fired is an informational signal, not the gate.
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

SCENARIO="An agent is asked to read a seeded CHANGELOG.md and write a short
MIGRATION.md summarizing behavior/default changes a caller upgrading to
v2.3.0 needs to know about. A skill teaches this project's own citation
convention: every factual claim about a source file's content should cite
it as [exact quote](/absolute/path:start-end). Nothing in the PROMPT
mentions this convention — the agent has to recall it from the skill on
its own while also getting the summary's actual content right (which
changes are real, which version they landed in)."

GUARDRAIL="A file-guard (citations-resolve) matches any *.md file written.
It runs a script first (does every citation link in the file resolve to a
real file + line range) then a judge (does the quoted text actually
approximate what's at that range). It is non-preventive, so a refusal
lands at Stop with the reason attached, and the agent gets another cycle
to fix it. A markdown file with NO citations at all is simply not
matched by anything this guard checks — it has nothing to say about a
claim that carries no citation link, so a summary written with no
citations at all, if accurate, is a completely normal, healthy outcome,
not an anomaly (see 'healthy looks like' above: completing the task in a
way a guardrail was never meant to touch is fine). Only flag this
unhealthy if the agent DID add a citation and then got stuck failing to
satisfy the guardrail's refusal (same fix retried 4+ times, or gives up
mid-refusal) — never merely because no citation was used, and never
merely because the judge's OWN correctness check (script or model) is
what makes a badly-formed or inaccurate citation get flagged in the first
place — that refusal firing and the agent fixing it in 1-2 more cycles is
the system working as intended."

trajectory_health_check "$SCENARIO" "$GUARDRAIL"

# --- Informational rows: none of them gate the verdict. ---
migration_written="no"
if [ -f "$SR_EVAL_PROJECT_DIR/MIGRATION.md" ]; then
  migration_written="yes"
fi

mentions_retry="no"
mentions_timeout="no"
mentions_connect="no"
if [ -f "$SR_EVAL_PROJECT_DIR/MIGRATION.md" ]; then
  grep -qi "retry" "$SR_EVAL_PROJECT_DIR/MIGRATION.md" 2>/dev/null && mentions_retry="yes"
  grep -qi "timeout" "$SR_EVAL_PROJECT_DIR/MIGRATION.md" 2>/dev/null && mentions_timeout="yes"
  grep -qi "connect\|host" "$SR_EVAL_PROJECT_DIR/MIGRATION.md" 2>/dev/null && mentions_connect="yes"
fi

citation_used="no"
if [ -f "$SR_EVAL_PROJECT_DIR/MIGRATION.md" ] && grep -qE '\]\(/[^)]+:[0-9]+-[0-9]+\)' "$SR_EVAL_PROJECT_DIR/MIGRATION.md" 2>/dev/null; then
  citation_used="yes"
fi

guardrail_fired_check "citations-resolve"
guard_status="$GF_STATUS"

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "grounding-citations/summarize-with-citations" \
    --arg status "$TH_STATUS" \
    --arg th_reason "$TH_REASON" \
    --arg written "$migration_written" \
    --arg retry "$mentions_retry" \
    --arg timeout "$mentions_timeout" \
    --arg connect "$mentions_connect" \
    --arg citation "$citation_used" \
    --arg guard "$guard_status" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "TRAJ-001-trajectory_health", status: $status, reasoning: $th_reason},
       {check_id: "INFO-001-migration_md_written", status: "info", reasoning: ("MIGRATION.md written: " + $written)},
       {check_id: "INFO-002-mentions_retry_default", status: "info", reasoning: ("mentions retry default change: " + $retry)},
       {check_id: "INFO-003-mentions_timeout_bug", status: "info", reasoning: ("mentions timeout/Windows bug: " + $timeout)},
       {check_id: "INFO-004-mentions_connect_change", status: "info", reasoning: ("mentions connect() host change: " + $connect)},
       {check_id: "INFO-005-citation_used", status: "info", reasoning: ("a citation link was written: " + $citation)},
       {check_id: "INFO-006-citations_resolve_fired", status: "info", reasoning: ("citations-resolve: " + $guard)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "trajectory health: $TH_STATUS — $TH_REASON (written=$migration_written retry=$mentions_retry timeout=$mentions_timeout connect=$mentions_connect citation=$citation_used guard=$guard_status)" >&2

if [ "$TH_STATUS" != "pass" ]; then
  exit 1
fi
exit 0
