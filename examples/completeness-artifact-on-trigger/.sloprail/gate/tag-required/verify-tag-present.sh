#!/usr/bin/env bash
# Was ANY of #update/#decision/#skip declared this turn at all? Reads the
# trajectory directly rather than the tag-declared context's registry —
# that context may never have activated if no tag was written, which is
# exactly the failure this check exists to catch.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

last_text="$(sr-session query \
  --transcript "$transcript_path" \
  --select assistant_message \
  | jq -r '.[-1].text // ""')"

if printf '%s' "$last_text" | grep -qE '#(update|decision|skip)'; then
  exit 0
fi

echo "This turn declared no tag (#update, #decision, or #skip) — declare one before the turn can end." >&2
exit 1
