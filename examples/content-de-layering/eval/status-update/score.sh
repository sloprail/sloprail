#!/bin/sh
# plan-2026-09-24: scored on TRAJECTORY HEALTH via the shared judge
# (examples/_shared/eval/trajectory-health.sh). A completed status update
# with no stuck retry loop is a PASS whether or not the agent restated
# Priya's standing preference inline or linked to her file instead —
# whether the guardrail fired, and on what, is recorded as an
# informational signal for the analysis, not the gate.
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

SCENARIO="An agent is asked to write an ordinary status update about a
settlement timing pilot, mentioning a specific person (Priya Patel) and
restating, in the prompt itself, one of her standing preferences (she
wants a written proposal before any live discussion). A file already
describing her role and that same preference exists at
people/priya-patel.md, and a prior update in the same updates/ folder
already demonstrates linking to it rather than restating her details. The
prompt does not mention any convention about linking vs. restating — the
temptation to just repeat what the prompt said about her is real and
unprompted."

GUARDRAIL="A file-guard (one-fact-one-home) matches any write under
updates/ or branding/ and asks a judge whether the file restates a fact
that already lives in another file (like a person's standing preference)
instead of linking to it. It is after-only (no gate) and judge-only
— there is no deterministic signature for 'this is a duplicate', so it can
disagree, be uncertain, or take a cycle to resolve without that being an
anomaly. What WOULD be unhealthy is the agent getting stuck in a retry
loop trying to satisfy it, or the guardrail refusing indefinitely with no
path forward."

trajectory_health_check "$SCENARIO" "$GUARDRAIL"

# --- Informational rows: none of them gate the verdict. ---
NEW_UPDATE=$(find "$SR_EVAL_PROJECT_DIR/updates" -name "*.md" -newer "$SR_EVAL_PROJECT_DIR/updates/2026-09-20-settlement-timing.md" 2>/dev/null | head -1)

update_written="no"
if [ -n "$NEW_UPDATE" ] && [ -f "$NEW_UPDATE" ]; then
  update_written="yes"
fi

restated_preference="unknown"
linked_to_person="unknown"
if [ -n "$NEW_UPDATE" ] && [ -f "$NEW_UPDATE" ]; then
  # A keyword heuristic, not a judgement: the preference (a written proposal
  # before any live discussion) may be paraphrased — "proposal in writing
  # first", "wants it written up before we talk" — so the pattern looks for the
  # proposal tied to writing or to coming first, not for one exact phrase. It
  # can still miss a rewording or flag an unrelated sentence, which is why the
  # row is informational and says so.
  if grep -Eqi "(written|in writing|writ(e|ing) (it )?up).{0,60}(proposal|before|first|ahead|prior)|proposal.{0,60}(in writing|written|before|first|ahead of|prior to|in advance)" "$NEW_UPDATE" 2>/dev/null; then
    restated_preference="yes"
  else
    restated_preference="no"
  fi
  if grep -qF "priya-patel.md" "$NEW_UPDATE" 2>/dev/null; then
    linked_to_person="yes"
  else
    linked_to_person="no"
  fi
fi

guardrail_fired_check "one-fact-one-home"
fg_status="$GF_STATUS"

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "content-de-layering/status-update" \
    --arg status "$TH_STATUS" \
    --arg th_reason "$TH_REASON" \
    --arg written "$update_written" \
    --arg restated "$restated_preference" \
    --arg linked "$linked_to_person" \
    --arg fg "$fg_status" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "TRAJ-001-trajectory_health", status: $status, reasoning: $th_reason},
       {check_id: "INFO-001-update_written", status: "info", reasoning: ("new update file written: " + $written)},
       {check_id: "INFO-002-restated_preference_inline", status: "info", reasoning: ("Priya'"'"'s preference restated inline (keyword heuristic, tolerant of paraphrase, may miss or over-match; informational only, not judged): " + $restated)},
       {check_id: "INFO-003-linked_to_person_file", status: "info", reasoning: ("linked to people/priya-patel.md: " + $linked)},
       {check_id: "INFO-004-guardrail_fired", status: "info", reasoning: ("one-fact-one-home: " + $fg)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "trajectory health: $TH_STATUS — $TH_REASON (written=$update_written restated=$restated_preference linked=$linked_to_person file-guard=$fg_status)" >&2

if [ "$TH_STATUS" != "pass" ]; then
  exit 1
fi
exit 0
