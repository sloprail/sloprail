#!/usr/bin/env bash
# Cheap first cut: every citation link in the new content must resolve to a
# real chunk of the trajectory/source — the link exists, before any judge
# is asked whether the quote actually matches it.
set -uo pipefail

input="$(cat)"
new="$(printf '%s' "$input" | jq -r 'if .event | has("newContent") then .event.newContent else null end')"

if [ "$new" = "null" ]; then
  exit 0
fi

# Citations are written as [text](/abs/path:start-end) — an absolute path,
# not a bare "jsonl:" tag (2026-08-19, his catch: a session can have MULTIPLE
# jsonl files over a file's lifetime, so a citation must pin WHICH transcript
# file it points into, not assume "the" one). A trajectory citation and a
# source-file citation are the same shape; only the path differs.
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
