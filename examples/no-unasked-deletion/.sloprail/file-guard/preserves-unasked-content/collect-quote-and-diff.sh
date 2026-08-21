#!/usr/bin/env bash
# prepare: the judge runs only when the script passed a real removal through —
# lines were removed AND the file's sr:asked quote resolved to the user's own
# words. Hand the judge exactly what it rules on: the authorizing quote, a
# unified DIFF of the change (not two full blobs — the diff IS what changed, so
# the judge sees the removed/added lines directly rather than diffing in its
# head), and — when the ask was an AskUserQuestion answer — the WHOLE answer
# ENVELOPE (the question and every answer), so the judge can weigh the removal
# against what was actually ASKED, not just the extracted answer. `diff` is POSIX,
# present on macOS and Linux. All nested under additionalContext, alongside the
# standard payload.
#
# WHY THE ENVELOPE. cite resolves a quote to a `<path>:<line>` and stops there —
# the location is all a citation needs. But the answer alone ("the second option")
# is not something a judge can weigh: it cannot tell WHAT WAS ASKED, nor which of
# several sibling answers the user meant, without the question beside it (PR-19
# review, cite.go:251). So this prepare, having a grounded quote, resolves it back
# to its line with cite and then fetches the whole envelope at that line with
# `sr-session trajectory envelope` (which wraps internal/transcript EnvelopeAt) —
# the full `The user answered: "<question>"="<answer>". ...` string, question
# included. A message-grounded quote (a plain user message, not an
# AskUserQuestion answer) has no envelope, and that is fine: asked_envelope is then
# empty and the judge weighs the quote and the diff alone.
set -uo pipefail

input="$(cat)"
old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"
new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath // ""')"
quote="$(printf '%s' "$input" \
  | jq -r '(.event.newMarkers // [])[] | select(.kind == "asked") | .fqn' \
  | head -1)"

# Unified diff, before -> after. diff exits 1 when they differ (they do — the
# script only lets a removal through), so guard the pipeline's exit.
change_diff="$(diff -u <(printf '%s' "$old") <(printf '%s' "$new") | tail -n +3 || true)"

# The whole answer envelope behind the ask, when there is one.
#
# Re-resolve the quote to its <path>:<line> with cite (the SCRIPT already proved
# it resolves to exactly one line; here we want that line, so we read cite's
# stdout rather than only its exit code) and fetch the envelope sitting there. A
# grounded quote that is a plain user message resolves fine but names no answer
# envelope, so `envelope` prints nothing — asked_envelope stays empty, which the
# template renders as "no envelope", not an error.
#
# Kept BEST-EFFORT: this is extra context for the judge, not a gate. If cite or
# envelope cannot resolve here (a citation the script grounded but this second
# lookup cannot, an unreadable trajectory), the judge still runs against the quote
# and the diff — the deterministic grounding already happened in the script, and a
# missing envelope must not turn a passed removal into a refusal.
asked_envelope=""
if [ -n "$quote" ] && [ -n "$transcript_path" ]; then
  # cite prints <path>:<line> for a single match. Take the first line (the script
  # guaranteed a single match; first is that match) and split off the trailing
  # :<line> — the path itself carries no colon in a ~/.claude/... transcript path,
  # but splitting on the LAST colon is correct regardless.
  citation="$(sr-session trajectory cite --path "$transcript_path" "$quote" 2>/dev/null | head -1)"
  if [ -n "$citation" ]; then
    cite_line="${citation##*:}"
    cite_path="${citation%:*}"
    if [ -n "$cite_line" ] && [ -n "$cite_path" ]; then
      asked_envelope="$(sr-session trajectory envelope --path "$cite_path" --line "$cite_line" 2>/dev/null || true)"
    fi
  fi
fi

jq -n \
  --arg quote "$quote" \
  --arg diff "$change_diff" \
  --arg envelope "$asked_envelope" \
  '{additionalContext: {asked_quote: $quote, change_diff: $diff, asked_envelope: $envelope}}'
