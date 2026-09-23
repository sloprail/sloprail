#!/usr/bin/env bash
# prepare: hand the judge what it rules on — the authorizing quote, a unified
# DIFF (not two full blobs, so it sees the changed lines directly), and, when the
# ask was an AskUserQuestion answer, the whole answer ENVELOPE. The envelope
# matters because an answer alone ("the second option") does not tell the judge
# WHAT WAS ASKED or which sibling answer was meant; `cite --include-envelope`
# resolves the quote and prints its envelope in one call. A message-grounded
# quote has no envelope — asked_envelope stays empty, which is fine.
set -uo pipefail

input="$(cat)"
old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"
new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath // ""')"
quote="$(printf '%s' "$input" \
  | jq -r '(.event.newMarkers // [])[] | select(.kind == "asked") | .fqn' \
  | head -1)"

# Unified diff, before -> after. diff exits 1 when they differ (they always do
# here), so guard the pipeline's exit.
change_diff="$(diff -u <(printf '%s' "$old") <(printf '%s' "$new") | tail -n +3 || true)"

# The answer envelope behind the ask, when there is one. `cite --include-envelope`
# prints the <path>:<line> citation, a blank line, then the envelope; drop the
# first two lines to keep the envelope. Best-effort: a failed lookup here must not
# turn a removal the script already grounded into a refusal, so the judge still
# runs on the quote and diff alone.
asked_envelope=""
if [ -n "$quote" ] && [ -n "$transcript_path" ]; then
  cite_out="$(sr-session trajectory cite --include-envelope --path "$transcript_path" "$quote" 2>/dev/null || true)"
  asked_envelope="$(printf '%s\n' "$cite_out" | tail -n +3)"
fi

jq -n \
  --arg quote "$quote" \
  --arg diff "$change_diff" \
  --arg envelope "$asked_envelope" \
  '{additionalContext: {asked_quote: $quote, change_diff: $diff, asked_envelope: $envelope}}'
