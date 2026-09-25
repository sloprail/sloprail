#!/usr/bin/env bash
# prepare: hand the judge what it rules on — the authorizing quote, a unified
# DIFF (not two full blobs, so it sees the changed lines directly), and, when the
# ask was an AskUserQuestion answer, the whole answer ENVELOPE. The envelope
# matters because an answer alone ("the second option") does not tell the judge
# WHAT WAS ASKED or which sibling answer was meant; `cite --include-envelope`
# resolves the quote and prints its envelope in one call. A message-grounded
# quote has no envelope — asked_envelope stays empty, which is fine.
#
# This only runs once removal-has-a-grounded-ask.sh has already permitted the
# write (resultKnown true), but reads resultKnown again here too rather than
# trust that ordering silently: a prepare step is not exempt from the same
# underivable-write trap its sibling script exists to guard against, and an
# empty additionalContext (rather than a misleading "the file became empty")
# is the correct output if this is ever reached with an unresolved write.
#
# ALSO computes pure_addition — REQUIRED, not cosmetic. Per this engine's own
# check-chain contract ("run in declared order, first REFUSAL ending it" —
# authoring-guardrails/script-checks.md), a script's exit 0 does NOT skip the
# checks after it; only a non-zero exit ends the chain. So when
# removal-has-a-grounded-ask.sh permits a PURE ADDITION (no lines removed, its
# own "no removed lines -> PASS" branch), this judge still runs next — and
# without pure_addition it would be asked to judge a diff with nothing removed
# against a quote that is correctly empty (there was nothing to authorize),
# and reasonably conclude "no grounded ask" and refuse a completely healthy
# append. Measured directly: a real Haiku run against this exact guard
# (examples/no-unasked-deletion/eval/add-section) hit precisely this — three
# identical PreToolUse refusals on a clean append-only edit, each blaming "the
# user's verbatim quote is empty", before the agent gave up and asked the user
# to change the hook's settings. change-is-clean-and-absolute.md.j2 auto-passes
# when pure_addition is true, closing this gap at the judge rather than trying
# (impossible, per the contract above) to make the script skip it.
set -uo pipefail

input="$(cat)"
old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"

# Content by event kind — the same fail-closed-on-Pre / trust-Post split
# removal-has-a-grounded-ask.sh uses. resultKnown is declared ONLY on the Pre
# kinds; reading it unconditionally (as an earlier version of this script did)
# defaults an absent field to false on EVERY Post recheck via the `// false`
# fallback, which returned an empty additionalContext (and so an empty
# asked_quote/change_diff reaching the judge) for a perfectly good, settled
# removal — exactly the same bug class the sibling script had, caught by a
# real e2e test (T049_11) once this file started branching on kind at all.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" != "true" ]; then
      jq -n '{additionalContext: {asked_quote: "", change_diff: "", asked_envelope: "", pure_addition: false}}'
      exit 0
    fi
    ;;
  PostFileCreate|PostFileUpdate)
    # Post always carries settled, derivable content — nothing to gate on.
    ;;
  *)
    jq -n '{additionalContext: {asked_quote: "", change_diff: "", asked_envelope: "", pure_addition: false}}'
    exit 0
    ;;
esac
new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath // ""')"
quote="$(printf '%s' "$input" \
  | jq -r '(.event.newMarkers // [])[] | select(.kind == "asked") | .fqn' \
  | head -1)"

# Same removed-lines test removal-has-a-grounded-ask.sh already ran (comm -23
# on the sorted, unique line sets) — recomputed here rather than passed
# through, since a prepare step's only input is this same CheckPayload.
removed_count="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"
pure_addition=false
[ "${removed_count:-0}" -eq 0 ] && pure_addition=true

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
  --argjson pure_addition "$pure_addition" \
  '{additionalContext: {asked_quote: $quote, change_diff: $diff, asked_envelope: $envelope, pure_addition: $pure_addition}}'
