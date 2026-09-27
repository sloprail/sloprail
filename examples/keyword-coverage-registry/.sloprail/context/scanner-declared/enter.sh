#!/usr/bin/env bash
# enter: parse the written scanner.yaml and, if active, log its FULL keyword set
# as one entry (not one per keyword) — the sibling gate checks "did ONE gh call
# cover ALL of these".
#
# Two moments log differently:
#   - Pre (the write is about to land): the entry becomes the UNION of what was
#     logged and what this write declares. The write may still be refused after
#     this runs (contexts enter before the preventive scanner-keywords-hold
#     guard), so a Pre write may add to the obligation but never shrink it.
#   - Post (the settled file, at Stop): the entry becomes exactly the file's
#     keywords. Anything that dropped one got past scanner-keywords-hold, which
#     asks for the user's words first.
# An entry is never removed: a declared scanner stays owed a search even if its
# file is later deleted or switched off — that is what verify-scanner-coverage
# reads, so deleting the file cannot make the obligation disappear.
set -uo pipefail

input="$(cat)"
scanner_path="$(printf '%s' "$input" | jq -r '.event.path // ""')"

# Content by event kind. On a Pre write with resultKnown false (a shell-derived
# write) the content is not derivable yet: decline (non-zero: this occurrence
# does not enter) and let the Post kind log the settled file at Stop.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    phase="post"
    content="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  PreFileCreate|PreFileUpdate)
    phase="pre"
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" != "true" ]; then
      # Result not derivable ahead of the write: defer to the Post kind.
      exit 1
    fi
    content="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  *)
    # No kind, or one this context is not about: nothing to activate on.
    exit 0
    ;;
esac

if [ -z "$content" ]; then
  [ "$phase" = "pre" ] && exit 1
  exit 0
fi

active="$(printf '%s' "$content" | grep '^active:' | awk '{print $2}')"
scanner_name="$(basename "$(dirname "$scanner_path")")"

if [ "$active" != "true" ]; then
  # Declared but not active — a scanner can be authored and left off. Before
  # the write there is nothing to log yet.
  [ "$phase" = "pre" ] && exit 1
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
  [ "$phase" = "pre" ] && exit 1
  jq -n --arg name "$scanner_name" '{scanner: $name, active: true, error: "keywords: is empty or missing"}'
  exit 0
fi

keywords_json="$(printf '%s' "$keywords" | jq -R -s 'split("\n") | map(select(length > 0))')"

if [ "$phase" = "pre" ]; then
  logged="$(sr-session state get "scanner:${scanner_name}" 2>/dev/null)"
  if printf '%s' "$logged" | jq -e 'type == "array"' >/dev/null 2>&1; then
    keywords_json="$(printf '%s' "$logged" | jq -c --argjson new "$keywords_json" '. + $new | unique')"
  fi
fi

sr-session state set "scanner:${scanner_name}" "$keywords_json"

jq -n --arg name "$scanner_name" --argjson kw "$keywords_json" \
  '{scanner: $name, active: true, keywords: $kw}'
