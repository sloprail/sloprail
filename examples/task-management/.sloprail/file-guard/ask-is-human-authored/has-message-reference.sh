#!/usr/bin/env bash
# Cheap gate: does the new content of ASK.md carry a reference to a human
# message at all? No reference at all is refused before any judge is asked
# whether the reference is true.
set -uo pipefail

input="$(cat)"
new="$(printf '%s' "$input" | jq -r 'if .event | has("newContent") then .event.newContent else null end')"

if [ "$new" = "null" ]; then
  cat <<EOF
{"decision":"block","reason":"Cannot verify a human-message reference on a write whose result this engine could not predict. Write ASK.md directly rather than through a command."}
EOF
  exit 1
fi

# A reference names a position in the record — a transcript range or a
# message id — not a bare claim like "the user asked for this".
if printf '%s' "$new" | grep -qE '(transcript_path|message_id|jsonl:[0-9]+)'; then
  exit 0
fi

cat <<EOF
{"decision":"block","reason":"ASK.md's content must carry a reference to the human message that authorised it (a transcript_path, message_id, or jsonl: line range) — content describing what to do, with no such reference, is content an agent could have written on its own."}
EOF
exit 1
