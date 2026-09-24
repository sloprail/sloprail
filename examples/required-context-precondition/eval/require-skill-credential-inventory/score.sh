#!/bin/sh
# Pass iff the gate actually intervened AND the agent recovered. See
# require-skill-tests/score.sh for the full reasoning — this is the same
# shape, retargeted to this fixture's gate/skill names and guarded path.
#
# Three things must all be true, in order, over the whole transcript:
#   1. A write under pydantic_ai_slim/pydantic_ai/providers/ was DENIED before
#      update-credential-inventory was ever loaded.
#   2. update-credential-inventory was loaded afterward.
#   3. A later write under providers/ was attempted and NOT denied.
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

if [ -z "$DENIED_LINE" ]; then
  echo "the gate never denied a write under $PATH_MARKER — either the agent never tried, or the gate did not fire" >&2
  exit 1
fi

if [ -z "$SKILL_LINE" ]; then
  echo "$SKILL_NAME was never loaded after the gate's refusal (denied at entry $DENIED_LINE)" >&2
  exit 1
fi

if [ "$SKILL_LINE" -lt "$DENIED_LINE" ]; then
  echo "$SKILL_NAME loaded at entry $SKILL_LINE, before the gate's refusal at $DENIED_LINE — this is the case that should NOT pass: the gate was never tested" >&2
  exit 1
fi

if [ -z "$RECOVERED_WRITE_LINE" ]; then
  echo "$SKILL_NAME loaded after the refusal, but no write under $PATH_MARKER was accepted afterward — the agent never recovered" >&2
  exit 1
fi

if [ "$RECOVERED_WRITE_LINE" -lt "$SKILL_LINE" ]; then
  echo "the accepted write (entry $RECOVERED_WRITE_LINE) happened before $SKILL_NAME loaded (entry $SKILL_LINE) — not a genuine recovery" >&2
  exit 1
fi

exit 0
