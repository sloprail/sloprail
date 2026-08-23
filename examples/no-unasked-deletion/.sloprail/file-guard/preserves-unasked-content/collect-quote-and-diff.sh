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
# review, cite.go:251). So this prepare, having a grounded quote, resolves it and
# fetches the whole envelope at that line in ONE call with
# `cite --include-envelope` (which prints the citation, then the envelope from
# internal/transcript EnvelopeAt) — the full
# `The user answered: "<question>"="<answer>". ...` string, question
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
# ONE call: `cite --include-envelope` resolves the quote to its <path>:<line> AND
# prints the answer envelope sitting at that line, separated from the citation by
# a blank line — so this does not cite and then resolve the same line a second
# time with `envelope`. The first output line is the <path>:<line> citation; the
# rest (after the blank line) is the envelope. A grounded quote that is a plain
# user message resolves fine but names no answer envelope, so nothing follows the
# citation — asked_envelope stays empty, which the template renders as "no
# envelope", not an error.
#
# Kept BEST-EFFORT: this is extra context for the judge, not a gate. If cite
# cannot resolve here (a citation the script grounded but this second lookup
# cannot, an unreadable trajectory), the judge still runs against the quote and
# the diff — the deterministic grounding already happened in the script, and a
# missing envelope must not turn a passed removal into a refusal.
asked_envelope=""
if [ -n "$quote" ] && [ -n "$transcript_path" ]; then
  cite_out="$(sr-session trajectory cite --include-envelope --path "$transcript_path" "$quote" 2>/dev/null || true)"
  # Drop the first line (the <path>:<line> citation) and the blank line after it;
  # what remains is the envelope, empty for a message-grounded quote.
  asked_envelope="$(printf '%s\n' "$cite_out" | tail -n +3)"
fi

jq -n \
  --arg quote "$quote" \
  --arg diff "$change_diff" \
  --arg envelope "$asked_envelope" \
  '{additionalContext: {asked_quote: $quote, change_diff: $diff, asked_envelope: $envelope}}'
