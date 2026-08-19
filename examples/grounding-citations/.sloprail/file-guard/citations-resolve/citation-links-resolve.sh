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

# Citations are written as [text](jsonl:START-END) or [text](path#Lstart-end).
citations="$(printf '%s' "$new" | grep -oE '\]\((jsonl:[0-9]+-[0-9]+|[^)]+#L[0-9]+-[0-9]+)\)' | sed 's/^](//; s/)$//')"

if [ -z "$citations" ]; then
  exit 0
fi

unresolved=""
while IFS= read -r ref; do
  [ -z "$ref" ] && continue
  case "$ref" in
    jsonl:*)
      range="${ref#jsonl:}"
      transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"
      start="${range%-*}"
      end="${range#*-}"
      if ! sed -n "${start},${end}p" "$transcript_path" > /dev/null 2>&1; then
        unresolved="$unresolved $ref"
      fi
      ;;
    *#L*)
      file="${ref%%#*}"
      [ -f "$file" ] || unresolved="$unresolved $ref"
      ;;
  esac
done <<< "$citations"

if [ -n "$unresolved" ]; then
  echo "These citations do not resolve to a real source:$unresolved" >&2
  exit 1
fi

exit 0
