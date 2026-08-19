#!/usr/bin/env bash
# prepare: the script already confirmed every citation link resolves — pull
# the actual cited text out of each source here, once, so the judge template
# never has to parse a citation link itself.
set -uo pipefail

input="$(cat)"
new="$(printf '%s' "$input" | jq -r 'if .event | has("newContent") then .event.newContent else null end')"

if [ "$new" = "null" ] || [ -z "$new" ]; then
  jq -n '{citations: []}'
  exit 0
fi

# Same shape citation-links-resolve.sh validated: [text](/abs/path:start-end).
matches="$(printf '%s' "$new" | grep -oE '\[[^]]*\]\(/[^)]+:[0-9]+-[0-9]+\)')"

pairs="[]"
while IFS= read -r m; do
  [ -z "$m" ] && continue
  quote="$(printf '%s' "$m" | sed -E 's/^\[([^]]*)\].*/\1/')"
  ref="$(printf '%s' "$m" | sed -E 's/^\[[^]]*\]\((.*)\)$/\1/')"
  file="${ref%:*}"
  range="${ref##*:}"
  start="${range%-*}"
  end="${range#*-}"

  source_text="$(sed -n "${start},${end}p" "$file" 2>/dev/null)"

  pairs="$(printf '%s' "$pairs" | jq --arg q "$quote" --arg r "$ref" --arg s "$source_text" \
    '. + [{quote: $q, reference: $r, source_text: $s}]')"
done <<< "$matches"

jq -n --argjson c "$pairs" '{citations: $c}'
