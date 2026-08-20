#!/usr/bin/env bash
# prepare: the judge only runs when the script above passed a removal through —
# i.e. lines were removed AND a human voiced some deletion/rewrite intent this
# turn. Hand the judge exactly the two things it rules on, so the template never
# parses a transcript or a diff itself: the removed lines, and the human
# messages that might authorize dropping them.
#
# Output nests under additionalContext (the one key a prepare's stdout is read
# under), alongside the standard payload the judge already has (event, etc.).
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"
old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"
new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"

removed_lines="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u))"

human_messages="$(sr-session trajectory normalize --path "$transcript_path" \
  | jq -r '[ .[]
      | select(.type == "user")
      | (.message
         | if type == "string" then .
           elif type == "array" then ([.[] | select(.type? == "text") | .text] | join(" "))
           else "" end) ]
      | map(select(length > 0))' 2>/dev/null)"

jq -n \
  --arg removed "$removed_lines" \
  --argjson asks "${human_messages:-[]}" \
  '{additionalContext: {removed_lines: $removed, human_messages: $asks}}'
