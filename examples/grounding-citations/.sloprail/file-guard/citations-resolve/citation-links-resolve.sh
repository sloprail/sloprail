#!/usr/bin/env bash
# Cheap first cut: every citation link in the new content must resolve to a
# real chunk of the trajectory/source — the link exists, before any judge
# is asked whether the quote actually matches it.
set -uo pipefail

input="$(cat)"

# Bytes chosen by event kind. This guard is an AFTER-check (file-guard.yaml
# declares no `preventive:`), so at runtime it only fires on the settled POST
# event, where content is always present. The Pre branches keep the script
# correct for any kind: `.event.resultKnown` tells "the update empties the file"
# from "the result was not derivable"; on an underivable Pre write, DEFER to the
# Post kind rather than guess.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    # Post carries the settled body — always present and derivable.
    new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  PreFileCreate|PreFileUpdate)
    # resultKnown can be false on a create as well as an update (a NotebookEdit
    # fresh .ipynb, or a command-derived edit), so an absent newContent here
    # must not be read as "empty".
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" != "true" ]; then
      # Result not derivable ahead of the write: defer to the Post kind, which
      # checks the settled content at Stop.
      exit 0
    fi
    new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  *)
    # No kind, or a kind this guard is not about (e.g. a delete): nothing to check.
    exit 0
    ;;
esac

if [ -z "$new" ]; then
  exit 0
fi

# Citations are written as [text](/abs/path:start-end) — an absolute path, not a
# bare id: a session can span multiple jsonl files, so a citation must pin WHICH
# transcript it points into. A trajectory citation and a source-file citation
# are the same shape; only the path differs.
citations="$(printf '%s' "$new" | grep -oE '\]\((/[^)]+:[0-9]+-[0-9]+)\)' | sed 's/^](//; s/)$//')"

if [ -z "$citations" ]; then
  exit 0
fi

unresolved=""
while IFS= read -r ref; do
  [ -z "$ref" ] && continue
  file="${ref%:*}"
  range="${ref##*:}"
  start="${range%-*}"
  end="${range#*-}"

  if [ ! -f "$file" ]; then
    unresolved="$unresolved $ref"
    continue
  fi

  if ! sed -n "${start},${end}p" "$file" > /dev/null 2>&1; then
    unresolved="$unresolved $ref"
  fi
done <<< "$citations"

if [ -n "$unresolved" ]; then
  echo "These citations do not resolve to a real source:$unresolved" >&2
  exit 1
fi

exit 0
