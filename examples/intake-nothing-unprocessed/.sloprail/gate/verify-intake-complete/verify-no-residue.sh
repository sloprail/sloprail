#!/usr/bin/env bash
# The residue pattern (unit 15's own words: same entity as deterministic
# refactoring, "I don't know how to semantically name it"). Collect every
# user message this turn, subtract those a task file references, refuse if
# anything is left.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

all_messages="$(sr-session query \
  --transcript "$transcript_path" \
  --select user_message \
  | jq -r '.[] | .id')"

if [ -z "$all_messages" ]; then
  exit 0
fi

# Every message a task file's ASK.md references (grep across the tree —
# this gate's script is an arbitrary executable, not limited to one file).
referenced="$(grep -rohE 'message_id=[A-Za-z0-9_-]+' tasks/ 2>/dev/null | sed 's/message_id=//' | sort -u)"

residue=""
while IFS= read -r id; do
  [ -z "$id" ] && continue
  if ! grep -qx "$id" <<< "$referenced"; then
    residue="$residue $id"
  fi
done <<< "$all_messages"

if [ -n "$residue" ]; then
  echo "These user messages are not mapped to any task, and nothing marked them as intentionally skipped:$residue" >&2
  exit 1
fi

exit 0
