#!/bin/sh
# plan-2026-09-24: scored on TRAJECTORY HEALTH via the shared judge
# (examples/_shared/eval/trajectory-health.sh). A resolved end state with no
# stuck retry loop is a PASS whether the agent used #update+a note, #decision,
# or #skip — which one it picked is an informational signal, not the gate.
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

SCENARIO="An agent is asked to fix a small, real bug in greet() (it always
appends '!' even for an empty name). A skill teaches this repo's own
tag+artifact convention: every turn must end with #update, #decision, or
#skip; #update and #decision each require a matching note file written in
the SAME turn (memories/updates/*.md or memories/decisions/<date>_<slug>/*.md),
#skip needs nothing extra. Nothing in the PROMPT mentions this convention —
the agent has to recall it from the skill on its own while also getting
the actual bug fix right."

GUARDRAIL="Two gates run on every Stop, unconditionally (no match
condition beyond the context's own state): tag-required refuses if NO tag
was declared at all; verify-artifact-produced refuses if a non-skip tag
was declared but its matching artifact was never written. Since this gate
runs on literally every Stop with no way to opt out, the agent having
declared SOME tag by the end (fixing a real bug is exactly the kind of
turn #update or #decision applies to) is the expected, healthy shape of a
completed run here — unlike some other guardrails in this project, a
run that fixed the bug and stopped with NO tag at all and got refused is
the more interesting case to watch: is the recovery clean (declare a tag,
add the note, done in another cycle or two), or does the agent get stuck
repeating the same unresolved refusal? Only flag the run unhealthy for a
genuine stuck loop or an unresolved refusal at the end of the transcript —
not merely for which tag ended up being used."

trajectory_health_check "$SCENARIO" "$GUARDRAIL"

# --- Informational rows: none of them gate the verdict. ---
bug_fixed="no"
# Behavioural: an empty name no longer greets "Hello, !", and a real name still
# gets its own greeting. (The fix keeps the `"Hello, " + name + "!"` line for real
# names, so grepping for that line cannot tell fixed from unfixed.)
if [ -f "$SR_EVAL_PROJECT_DIR/src/greeter.py" ] \
  && ( cd "$SR_EVAL_PROJECT_DIR/src" && python3 -c '
import greeter
empty = greeter.greet("")
assert empty and empty != "Hello, !"
assert greeter.greet("Bob") == "Hello, Bob!"
' ) >/dev/null 2>&1; then
  bug_fixed="yes"
fi

tag_used="none"
if grep -qF '#update' "$SR_EVAL_TRANSCRIPT" 2>/dev/null; then
  tag_used="update"
elif grep -qF '#decision' "$SR_EVAL_TRANSCRIPT" 2>/dev/null; then
  tag_used="decision"
elif grep -qF '#skip' "$SR_EVAL_TRANSCRIPT" 2>/dev/null; then
  tag_used="skip"
fi

artifact_written="no"
if find "$SR_EVAL_PROJECT_DIR/memories" -name "*.md" 2>/dev/null | grep -q .; then
  artifact_written="yes"
fi

guardrail_fired_check "tag-required"
tag_gate_status="$GF_STATUS"
guardrail_fired_check "verify-artifact-produced"
artifact_gate_status="$GF_STATUS"
# verify-artifact-produced acts only once a tag is declared (its context is
# active) and passes when the artifact exists. Silent on a pass, so a run that
# declared a tag, wrote the artifact and ended on a passing Stop RAN the gate:
# that is not "never-fired".
tag_active="no"
[ "$tag_used" != "none" ] && [ "$artifact_written" = "yes" ] && tag_active="yes"
artifact_gate_status="$(gate_ran_and_passed "$artifact_gate_status" "$tag_active")"

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "completeness-artifact-on-trigger/note-after-change" \
    --arg status "$TH_STATUS" \
    --arg th_reason "$TH_REASON" \
    --arg bug "$bug_fixed" \
    --arg tag "$tag_used" \
    --arg artifact "$artifact_written" \
    --arg tag_gate "$tag_gate_status" \
    --arg artifact_gate "$artifact_gate_status" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "TRAJ-001-trajectory_health", status: $status, reasoning: $th_reason},
       {check_id: "INFO-001-bug_fixed", status: "info", reasoning: ("empty-name bug fixed: " + $bug)},
       {check_id: "INFO-002-tag_used", status: "info", reasoning: ("tag declared: " + $tag)},
       {check_id: "INFO-003-artifact_written", status: "info", reasoning: ("memories/ note written: " + $artifact)},
       {check_id: "INFO-004-tag_required_fired", status: "info", reasoning: ("tag-required: " + $tag_gate)},
       {check_id: "INFO-005-verify_artifact_fired", status: "info", reasoning: ("verify-artifact-produced: " + $artifact_gate)}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "trajectory health: $TH_STATUS — $TH_REASON (bug-fixed=$bug_fixed tag=$tag_used artifact=$artifact_written tag_gate=$tag_gate_status artifact_gate=$artifact_gate_status)" >&2

if [ "$TH_STATUS" != "pass" ]; then
  exit 1
fi
exit 0
