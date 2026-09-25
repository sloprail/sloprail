#!/usr/bin/env bash
# enter: parse the written scanner.yaml and, if active, log its FULL keyword set
# as one entry (not one per keyword) — the sibling gate checks "did ONE gh call
# cover ALL of these".
set -uo pipefail

input="$(cat)"
scanner_path="$(printf '%s' "$input" | jq -r '.event.path // ""')"

# Content by event kind. Post runs in practice; the Pre branches keep the script
# correct for any kind. On a Pre write with resultKnown false, the content is not
# derivable yet — defer to the Post kind rather than mistake it for an empty file.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    content="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" != "true" ]; then
      # Result not derivable ahead of the write: defer to the Post kind.
      exit 0
    fi
    content="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  *)
    # No kind, or one this context is not about: nothing to activate on.
    exit 0
    ;;
esac

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
