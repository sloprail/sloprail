#!/usr/bin/env bash
# Cheap gate: does ASK.md's new content reference a human message at all?
# Refuse when absent, before the judge is asked whether the reference is true.
set -uo pipefail

input="$(cat)"

# resultKnown, not has("newContent") — newContent is ALWAYS a present key on
# PreFileCreate/PreFileUpdate (the flat-event-fields discipline), so
# `has("newContent")` is always true and this branch could never fire as
# written; the actual "this engine could not predict the result" signal
# (a command-derived edit, a NotebookEdit fresh-.ipynb) is resultKnown, which
# was never consulted at all. On a Post kind resultKnown is absent and the
# bytes are always settled, so only gate on it when it is explicitly false.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" != "true" ]; then
      echo '{"reason":"Cannot verify a human-message reference on a write whose result this engine could not predict. Write ASK.md directly rather than through a command."}'
      exit 1
    fi
    ;;
esac

new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"

# A reference names a position in the record — a transcript range or a
# message id — not a bare claim like "the user asked for this".
if printf '%s' "$new" | grep -qE '(transcript_path|message_id|jsonl:[0-9]+)'; then
  exit 0
fi

echo '{"reason":"ASK.md'"'"'s content must carry a reference to the human message that authorised it (a transcript_path, message_id, or jsonl: line range) — content describing what to do, with no such reference, is content an agent could have written on its own."}'
exit 1
