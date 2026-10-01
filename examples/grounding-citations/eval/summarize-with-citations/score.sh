#!/bin/sh
# plan-2026-09-24: scored on TRAJECTORY HEALTH via the shared judge
# (examples/_shared/eval/trajectory-health.sh). An accurate summary with no
# stuck retry loop is a PASS — whether the file-guard fired, and whether the
# agent cited on its first try, are informational signals, not the gate.
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
convention: a markdown file is written with sr-file, citing the exact words
of the tool output it restates (--cite:tool_result '<quote>', e.g. from the
Read of CHANGELOG.md) on the command, never inside the file. Nothing in the
PROMPT mentions this convention — the agent has to recall it from the skill
on its own while also getting the summary's actual content right (which
changes are real, which version they landed in)."

GUARDRAIL="A gate (citations-resolve, before the write) and a file-guard of the same
name (the Stop after-check) match any *.md file written. The gate requires every write to carry a citation of tool output: a write
with none (the Write tool, a shell redirect) is refused before it lands, and
the refusal names the sr-file form. A cited write is then judged at Stop, by the file-guard, which
reads each quote with the full tool output it came from and asks whether the
file's claims say what that output says. A first write refused for having no
citation, followed by the agent reading the skill or the refusal and writing
it with sr-file within a cycle or two, is the system working as intended, not
an anomaly. A quote refused as not resolving (it is not word for word in any
tool output, or matches several) and fixed within a try or two is healthy
too. Only flag this unhealthy if the agent gets stuck — the same refused write
retried 4+ times with no change in approach — or gives up without writing an
accurate MIGRATION.md."

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
if grep -q -- '--cite:tool_result' "$SR_EVAL_TRANSCRIPT" 2>/dev/null; then
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
       {check_id: "INFO-005-citation_used", status: "info", reasoning: ("an sr-file --cite:tool_result was run: " + $citation)},
       {check_id: "INFO-006-citations_resolve_fired", status: "info", reasoning: ("citations-resolve: " + $guard)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "trajectory health: $TH_STATUS — $TH_REASON (written=$migration_written retry=$mentions_retry timeout=$mentions_timeout connect=$mentions_connect citation=$citation_used guard=$guard_status)" >&2

if [ "$TH_STATUS" != "pass" ]; then
  exit 1
fi
exit 0
