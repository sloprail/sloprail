#!/usr/bin/env bash
# The live prototype named in the raw unit: the memory hooks' hashtag rule.
# A turn must declare ONE of #update/#decision/#skip, and if it declared
# one that demands an artifact, that artifact must actually exist —
# absence is the violation, not malformed content (that's a different rule
# type, conformance).
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

last_text="$(sr-session query \
  --transcript "$transcript_path" \
  --select assistant_message \
  | jq -r '.[-1].text // ""')"

if printf '%s' "$last_text" | grep -q '#skip'; then
  exit 0
fi

tag=""
artifact_glob=""
if printf '%s' "$last_text" | grep -q '#update'; then
  tag="#update"
  artifact_glob="memories/updates/*.md"
elif printf '%s' "$last_text" | grep -q '#decision'; then
  tag="#decision"
  artifact_glob="memories/decisions/*/DECISION.md"
fi

if [ -z "$tag" ]; then
  echo "This turn declared no tag (#update, #decision, or #skip) — declare one and produce the artifact it demands." >&2
  exit 1
fi

# The artifact must have been touched THIS turn — a stale file from a prior
# turn does not satisfy "produced on trigger".
touched="$(sr-session query \
  --transcript "$transcript_path" \
  --select tool_use \
  --where 'name == "Write" or name == "Edit"' \
  | jq -r --arg glob "$artifact_glob" '[.[] | select(.input.file_path? != null)] | length')"

if [ "${touched:-0}" -eq 0 ]; then
  echo "Turn declared $tag but no matching artifact was written this turn — the tag was stated, the artifact was not produced." >&2
  exit 1
fi

exit 0
