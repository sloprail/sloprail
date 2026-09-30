#!/usr/bin/env bash
# Refuse a write whose result the engine could not derive (resultKnown false: sed -i,
# a notebook create, an unresolvable sr-file line). event.newContent is then "" —
# indistinguishable from an emptied file — and the judge would be judging nothing.
# A gate does not fail closed on that by itself, so this check does.
set -uo pipefail
payload="$(cat)"
if [ "$(printf '%s' "$payload" | jq -r '.event.resultKnown // false')" != "true" ]; then
  path="$(printf '%s' "$payload" | jq -r '.event.path // ""')"
  echo "eval-score-reads-transcript: the result of this write to $path could not be derived (resultKnown false), so the scorer could not be judged before it lands. Write the file's content directly (Write/Edit), or use sr-file on its own, instead of a command that edits it in place." >&2
  exit 1
fi
