#!/usr/bin/env bash
# prepare: the judge runs only when the script passed a real removal through —
# lines were removed AND the file's sr:asked quote resolved to the user's own
# words. Hand the judge exactly what it rules on: the authorizing quote, and a
# unified DIFF of the change (not two full blobs — the diff IS what changed, so
# the judge sees the removed/added lines directly rather than diffing in its
# head). `diff` is POSIX, present on macOS and Linux. Nested under
# additionalContext, alongside the standard payload.
set -uo pipefail

input="$(cat)"
old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"
new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
quote="$(printf '%s' "$input" \
  | jq -r '(.event.newMarkers // [])[] | select(.kind == "asked") | .fqn' \
  | head -1)"

# Unified diff, before -> after. diff exits 1 when they differ (they do — the
# script only lets a removal through), so guard the pipeline's exit.
change_diff="$(diff -u <(printf '%s' "$old") <(printf '%s' "$new") | tail -n +3 || true)"

jq -n \
  --arg quote "$quote" \
  --arg diff "$change_diff" \
  '{additionalContext: {asked_quote: $quote, change_diff: $diff}}'
