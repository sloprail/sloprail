#!/usr/bin/env bash
# enter: parse the written scanner.yaml and, if active, log its FULL keyword set
# as one entry (not one per keyword) — the sibling gate checks "did ONE gh call
# cover ALL of these".
set -uo pipefail

input="$(cat)"
content="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
scanner_path="$(printf '%s' "$input" | jq -r '.event.path // ""')"

if [ -z "$content" ]; then
  exit 0
fi

active="$(printf '%s' "$content" | grep '^active:' | awk '{print $2}')"
scanner_name="$(basename "$(dirname "$scanner_path")")"

if [ "$active" != "true" ]; then
  # Declared but not active — a scanner can be authored and left off.
  jq -n --arg name "$scanner_name" '{scanner: $name, active: false}'
  exit 0
fi

# keywords: is a YAML list under the key. Strip the leading "- " and any
# surrounding quotes (bare or quoted scalars both).
keywords="$(printf '%s' "$content" \
  | sed -n '/^keywords:/,/^[a-z]/p' \
  | grep -E '^[[:space:]]*-[[:space:]]' \
  | sed -E 's/^[[:space:]]*-[[:space:]]*//; s/^"(.*)"$/\1/; s/^'"'"'(.*)'"'"'$/\1/')"

if [ -z "$keywords" ]; then
  jq -n --arg name "$scanner_name" '{scanner: $name, active: true, error: "keywords: is empty or missing"}'
  exit 0
fi

keywords_json="$(printf '%s' "$keywords" | jq -R -s 'split("\n") | map(select(length > 0))')"
sr-session state set "scanner:${scanner_name}" "$keywords_json"

jq -n --arg name "$scanner_name" --argjson kw "$keywords_json" \
  '{scanner: $name, active: true, keywords: $kw}'
