#!/usr/bin/env bash
# enter: a scanner.yaml was written under scanners/ (the trigger's `match`
# already confirmed the path). Parse { active: bool, keywords: str[] } and, only
# if active, log the scanner's FULL keyword set as one entry — all its keywords
# together, not one entry per keyword, because the sibling gate checks "did ONE
# gh call cover ALL of these", not "did each keyword appear somewhere".
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

# keywords: is a YAML list, one `- term` per line, indented under the key.
# Quoted or bare scalars both — the surrounding quotes are stripped, not
# just the leading "- ".
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
