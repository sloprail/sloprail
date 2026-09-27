#!/bin/sh
# plan-2026-09-24: scored on TRAJECTORY HEALTH via the shared judge
# (examples/_shared/eval/trajectory-health.sh). A genuine investigation with
# no stuck retry loop is a PASS whether or not the agent used the scanner
# convention at all — whether the gate fired is an informational signal, not
# the bar. Uses the real gh CLI against the real GitHub API.
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

SCENARIO="An agent is asked to check GitHub for real prior art on auth
tokens leaking into logs, then write a short SCAN-NOTES.md summarizing
what it found. Whatever it searches with runs for real — gh against the real
GitHub API, or a web search — so what turns up is genuinely unscripted; the
prompt names no tool, so either is a legitimate way to do the task. A skill teaches
this project's own scanner-declaration convention: write
scanners/<name>/scanner.yaml naming the keywords a topic requires, then
cover ALL of them together in ONE gh search call rather than splitting
them across several searches. Nothing in the PROMPT mentions this
convention — the agent has to recall it from the skill on its own while
also doing a genuinely useful search and writing an accurate summary."

GUARDRAIL="A gate (verify-scanner-coverage) fires at Stop only when a
scanner was declared this session (context match skips otherwise). It
refuses if no single gh call's query text contained every one of the
declared scanner's keywords together. A markdown file written with NO
scanner ever declared is simply not matched by anything this guard checks
— it has nothing to say about a search that carries no scanner
declaration, so an investigation that used gh directly, or a web search and
no gh at all, and wrote an accurate SCAN-NOTES.md without ever declaring a
scanner is a completely normal, healthy outcome, not an anomaly (see 'healthy looks like' above:
completing the task in a way a guardrail was never meant to touch is
fine). Only flag this unhealthy if the agent DID declare a scanner and
then got stuck failing to satisfy the guardrail's refusal (same fix
retried 4+ times, or gives up mid-refusal) — never merely because no
scanner was declared, and never merely because a real gh search came back
with few or no useful results (that is a fact about GitHub's real content,
not an agent failure)."

trajectory_health_check "$SCENARIO" "$GUARDRAIL"

# --- Informational rows: none of them gate the verdict. ---
notes_written="no"
if [ -f "$SR_EVAL_PROJECT_DIR/SCAN-NOTES.md" ]; then
  notes_written="yes"
fi

scanner_declared="no"
if [ -d "$SR_EVAL_PROJECT_DIR/scanners" ] && find "$SR_EVAL_PROJECT_DIR/scanners" -name "scanner.yaml" 2>/dev/null | grep -q .; then
  scanner_declared="yes"
fi

gh_used="no"
if grep -qF '"bin":"gh"' "$SR_EVAL_TRANSCRIPT" 2>/dev/null || grep -qF 'gh search\|gh api' "$SR_EVAL_TRANSCRIPT" 2>/dev/null; then
  gh_used="yes"
fi

guardrail_fired_check "verify-scanner-coverage"
guard_status="$GF_STATUS"

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "keyword-coverage-registry/security-scan" \
    --arg status "$TH_STATUS" \
    --arg th_reason "$TH_REASON" \
    --arg notes "$notes_written" \
    --arg scanner "$scanner_declared" \
    --arg gh "$gh_used" \
    --arg guard "$guard_status" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "TRAJ-001-trajectory_health", status: $status, reasoning: $th_reason},
       {check_id: "INFO-001-scan_notes_written", status: "info", reasoning: ("SCAN-NOTES.md written: " + $notes)},
       {check_id: "INFO-002-scanner_declared", status: "info", reasoning: ("scanners/*/scanner.yaml written: " + $scanner)},
       {check_id: "INFO-003-gh_used", status: "info", reasoning: ("a real gh call was made: " + $gh)},
       {check_id: "INFO-004-verify_scanner_coverage_fired", status: "info", reasoning: ("verify-scanner-coverage: " + $guard)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "trajectory health: $TH_STATUS — $TH_REASON (notes=$notes_written scanner=$scanner_declared gh=$gh_used guard=$guard_status)" >&2

if [ "$TH_STATUS" != "pass" ]; then
  exit 1
fi
exit 0
