#!/usr/bin/env bash
# prepare: hand the judge what it rules on — the user's words the change CITES
# (off `.event.citations`, already resolved against the session's record), a
# unified DIFF (not two full blobs, so it sees the changed lines directly), and,
# when a citation landed on an AskUserQuestion answer, the whole answer
# ENVELOPE. The envelope matters because an answer alone ("the second option")
# does not tell the judge WHAT WAS ASKED or which sibling answer was meant;
# `cite --include-envelope` prints it. A message-grounded citation has no
# envelope — asked_envelope stays empty, which is fine.
#
# ALSO computes pure_addition — REQUIRED, not cosmetic. A script's exit 0 does
# NOT skip the checks after it; only a refusal ends the chain. So when
# removal-has-a-grounded-ask.sh permits a PURE ADDITION, this judge still runs
# next — and without pure_addition it would judge a diff with nothing removed
# against no citation (correctly none: nothing needed authorizing) and refuse a
# healthy append. Measured against a real Haiku run (eval/add-section).
# change-is-clean-and-absolute.md.j2 auto-passes when pure_addition is true.
set -uo pipefail

input="$(cat)"
old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"

empty() {
  jq -n '{additionalContext: {asked_quotes: [], change_diff: "", asked_envelope: "", pure_addition: false}}'
  exit 0
}

# Content by event kind — the same fail-closed-on-Pre / trust-Post split
# removal-has-a-grounded-ask.sh uses. resultKnown is declared ONLY on the Pre
# create/update kinds; reading it on a Post kind defaults it to false and hands
# the judge an empty context for a perfectly good, settled change.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    [ "$known" = "true" ] || empty
    new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  PostFileCreate|PostFileUpdate)
    new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  PreFileDelete|PostFileDelete)
    new=""
    ;;
  *)
    empty
    ;;
esac

quotes="$(printf '%s' "$input" \
  | jq -c '[(.event.citations // [])[] | select((.sourceTypes // []) | index("user")) | .quote]')"

# Same removed-lines test removal-has-a-grounded-ask.sh ran — recomputed rather
# than passed through, since a prepare step's only input is this same payload.
removed_count="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"
pure_addition=false
[ "${removed_count:-0}" -eq 0 ] && pure_addition=true

# Unified diff, before -> after. diff exits 1 when they differ, so guard it.
change_diff="$(diff -u <(printf '%s' "$old") <(printf '%s' "$new") | tail -n +3 || true)"

# The answer envelope behind the first user citation, when it landed on one.
# Best-effort: a failed lookup must not turn a cited removal into a refusal, so
# the judge still runs on the quotes and diff alone.
asked_envelope=""
first='[(.event.citations // [])[] | select((.sourceTypes // []) | index("user"))][0] // {}'
cited_path="$(printf '%s' "$input" | jq -r "($first).path // empty")"
cited_quote="$(printf '%s' "$input" | jq -r "($first).quote // empty")"
if [ -n "$cited_path" ] && [ -n "$cited_quote" ]; then
  cite_out="$(sr-session trajectory cite --include-envelope --path "$cited_path" "$cited_quote" 2>/dev/null || true)"
  asked_envelope="$(printf '%s\n' "$cite_out" | tail -n +3)"
fi

jq -n \
  --argjson quotes "$quotes" \
  --arg diff "$change_diff" \
  --arg envelope "$asked_envelope" \
  --argjson pure_addition "$pure_addition" \
  '{additionalContext: {asked_quotes: $quotes, change_diff: $diff, asked_envelope: $envelope, pure_addition: $pure_addition}}'
