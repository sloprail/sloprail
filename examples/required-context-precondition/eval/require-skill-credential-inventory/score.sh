#!/bin/sh
# Pass iff the gate actually intervened AND the agent recovered. See
# require-skill-tests/score.sh for the full reasoning — this is the same
# shape, retargeted to this fixture's gate/skill names and guarded path.
#
# Three checks, each independent, over the whole transcript:
#   CI-001 denied_first_attempt      — a write under providers/ was DENIED
#                                       before the skill was ever loaded
#   CI-002 skill_loaded_after_denial — the skill was loaded AFTER that denial
#   CI-003 recovered_write           — a later write under providers/ was
#                                       accepted, not denied, after the load
#
# Every check's own pass/fail and reasoning is written to
# $SR_EVAL_VERDICT_OUT, pass or fail, whole-run — {subject, status, rows},
# a10n-eval's own scorer verdict.json shape, unchanged field names. Exit
# code stays the authority sr-eval itself reads.
set -eu

GATE_MARKER='gate "require-skill-credential-inventory"'
SKILL_NAME="update-credential-inventory"
PATH_MARKER="pydantic_ai_slim/pydantic_ai/providers/"

if [ -z "${SR_EVAL_TRANSCRIPT:-}" ]; then
  echo "SR_EVAL_TRANSCRIPT not set" >&2
  exit 1
fi
if [ -z "${SR_EVAL_BIN_DIR:-}" ]; then
  echo "SR_EVAL_BIN_DIR not set" >&2
  exit 1
fi

SR_SESSION="$SR_EVAL_BIN_DIR/sr-session"

ENTRIES_FILE=$(mktemp)
trap 'rm -f "$ENTRIES_FILE"' EXIT
printf '{"transcript_path":"%s"}' "$SR_EVAL_TRANSCRIPT" | "$SR_SESSION" query --whole-session > "$ENTRIES_FILE"

DENIED_LINE=$(jq -r --arg gate "$GATE_MARKER" '
  to_entries[]
  | select(.value.message.content[]?.type == "tool_result")
  | select((.value.message.content[]?.content // "" | tostring) | contains($gate))
  | .key' "$ENTRIES_FILE" | head -1)

SKILL_LINE=$(jq -r --arg skill "$SKILL_NAME" '
  to_entries[]
  | select(.value.message.content[]?.type == "tool_use")
  | select(.value.message.content[]?.name == "Skill")
  | select(.value.message.content[]?.input.skill == $skill)
  | .key' "$ENTRIES_FILE" | head -1)

RECOVERED_WRITE_LINE=$(jq -r --arg gate "$GATE_MARKER" --arg pm "$PATH_MARKER" '
  to_entries as $all
  | $all[]
  | select(.value.message.content[]?.type == "tool_use")
  | select(.value.message.content[]?.name == "Write" or .value.message.content[]?.name == "Edit")
  | select(.value.message.content[]?.input.file_path // "" | contains($pm))
  | . as $call
  | ($all[] | select(.value.parentUuid == $call.value.uuid) | .value) as $reply
  | select(($reply.message.content[]?.content // "" | tostring) | contains($gate) | not)
  | $call.key' "$ENTRIES_FILE" | head -1)

# --- CI-001: denied_first_attempt ---
if [ -n "$DENIED_LINE" ]; then
  ci001_status="pass"
  ci001_reason="the gate denied a write under $PATH_MARKER at transcript entry $DENIED_LINE"
else
  ci001_status="fail"
  ci001_reason="the gate never denied a write under $PATH_MARKER — either the agent never tried, or the gate did not fire"
fi

# --- CI-002: skill_loaded_after_denial ---
if [ -z "$SKILL_LINE" ]; then
  ci002_status="fail"
  ci002_reason="$SKILL_NAME was never loaded"
elif [ -z "$DENIED_LINE" ]; then
  ci002_status="fail"
  ci002_reason="$SKILL_NAME loaded at entry $SKILL_LINE, but CI-001 never denied anything to load it after"
elif [ "$SKILL_LINE" -lt "$DENIED_LINE" ]; then
  ci002_status="fail"
  ci002_reason="$SKILL_NAME loaded at entry $SKILL_LINE, BEFORE the gate's refusal at $DENIED_LINE — the gate was never tested"
else
  ci002_status="pass"
  ci002_reason="$SKILL_NAME loaded at entry $SKILL_LINE, after the refusal at $DENIED_LINE"
fi

# --- CI-003: recovered_write ---
if [ -z "$RECOVERED_WRITE_LINE" ]; then
  ci003_status="fail"
  ci003_reason="no write under $PATH_MARKER was ever accepted"
elif [ "$ci002_status" != "pass" ]; then
  ci003_status="fail"
  ci003_reason="a write under $PATH_MARKER was accepted at entry $RECOVERED_WRITE_LINE, but CI-002 did not pass — not a genuine recovery"
elif [ "$RECOVERED_WRITE_LINE" -lt "$SKILL_LINE" ]; then
  ci003_status="fail"
  ci003_reason="the accepted write at entry $RECOVERED_WRITE_LINE happened BEFORE $SKILL_NAME loaded at entry $SKILL_LINE"
else
  ci003_status="pass"
  ci003_reason="a write under $PATH_MARKER was accepted at entry $RECOVERED_WRITE_LINE, after $SKILL_NAME loaded at entry $SKILL_LINE"
fi

overall="pass"
[ "$ci001_status" = "pass" ] && [ "$ci002_status" = "pass" ] && [ "$ci003_status" = "pass" ] || overall="fail"

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "require-skill-credential-inventory" \
    --arg status "$overall" \
    --arg ci001s "$ci001_status" --arg ci001r "$ci001_reason" \
    --arg ci002s "$ci002_status" --arg ci002r "$ci002_reason" \
    --arg ci003s "$ci003_status" --arg ci003r "$ci003_reason" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "CI-001-denied_first_attempt", status: $ci001s, reasoning: $ci001r},
       {check_id: "CI-002-skill_loaded_after_denial", status: $ci002s, reasoning: $ci002r},
       {check_id: "CI-003-recovered_write", status: $ci003s, reasoning: $ci003r}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

if [ "$overall" != "pass" ]; then
  echo "$ci001_reason; $ci002_reason; $ci003_reason" >&2
  exit 1
fi

exit 0
