#!/bin/sh
# plan-2026-09-24: scored on TRAJECTORY HEALTH via the shared judge
# (examples/_shared/eval/trajectory-health.sh). A working PreCompact
# implementation with no stuck retry loop is a PASS whether or not the agent
# used the sr:docs marker, checked the real docs itself (WebFetch is
# available to it directly, unprompted), or got a detail wrong — whether the
# mock-matches-doc guardrail ever activated, and what it found, is recorded
# as an informational signal for the analysis, not the gate.
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

SCENARIO="An agent is working in claude-mock, a real Go codebase that
emulates Claude Code's hook behavior for testing. It is asked to add
support for a hook event (PreCompact) the mock is missing, matching how the
existing hook events in the codebase are structured (internal/hooks/event.go's
EventName constants, the runner's firing logic around an isCompactSummary
record). Nothing in the prompt mentions checking documentation, a marker
convention, or sloprail — this is meant to read as an ordinary feature
request in an unfamiliar codebase."

GUARDRAIL="A file-guard (mock-matches-doc) exists to catch code that claims,
via an sr:docs <URL> marker comment, to conform to a real doc but actually
drifts from it. It ONLY activates if the agent writes that specific marker
comment — the project's CLAUDE.md states that convention (sr:docs beside
code that emulates Claude Code; the tree's a10n:docs comments are an older
spelling) and a project skill teaches the marker's mechanism, nothing about
what any specific doc says; the prompt itself never mentions it. Whether it
activates or not says nothing about whether the implementation is correct;
Claude Code's real hooks documentation is public and fetchable via
WebFetch, which the agent has unprompted access to regardless of the
marker."

trajectory_health_check "$SCENARIO" "$GUARDRAIL"

# --- Informational rows: none of them gate the verdict. ---
event_go="$SR_EVAL_PROJECT_DIR/claude-mock/internal/hooks/event.go"

precompact_added="no"
if [ -f "$event_go" ] && grep -qF 'PreCompact' "$event_go" 2>/dev/null; then
  precompact_added="yes"
fi

marker_used="no"
if grep -qF 'sr:docs' "$SR_EVAL_TRANSCRIPT" 2>/dev/null; then
  marker_used="yes"
fi

webfetch_used="no"
if grep -qF '"WebFetch"' "$SR_EVAL_TRANSCRIPT" 2>/dev/null; then
  webfetch_used="yes"
fi

guardrail_fired_check "mock-matches-doc"
fg_status="$GF_STATUS"

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "doc-conformance/precompact-support" \
    --arg status "$TH_STATUS" \
    --arg th_reason "$TH_REASON" \
    --arg added "$precompact_added" \
    --arg marker "$marker_used" \
    --arg webfetch "$webfetch_used" \
    --arg fg "$fg_status" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "TRAJ-001-trajectory_health", status: $status, reasoning: $th_reason},
       {check_id: "INFO-001-precompact_added", status: "info", reasoning: ("PreCompact event constant added: " + $added)},
       {check_id: "INFO-002-docs_marker_used", status: "info", reasoning: ("sr:docs marker used: " + $marker)},
       {check_id: "INFO-003-webfetch_used", status: "info", reasoning: ("agent used WebFetch (checked real docs on its own): " + $webfetch)},
       {check_id: "INFO-004-guardrail_fired", status: "info", reasoning: ("mock-matches-doc: " + $fg)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "trajectory health: $TH_STATUS — $TH_REASON (precompact-added=$precompact_added marker=$marker_used webfetch=$webfetch_used file-guard=$fg_status)" >&2

if [ "$TH_STATUS" != "pass" ]; then
  exit 1
fi
exit 0
