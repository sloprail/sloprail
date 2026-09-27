#!/usr/bin/env bash
# prepare: hand the judge the unified DIFF it rules on (not two full blobs, so
# it sees the changed lines directly). The cited words need no preparing: the
# judge template reads them straight off `.event.citations`.
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
# oldContent exists only on the update and delete kinds; a create has nothing
# before it, so its prior content is empty by construction, not by default.
old="$(printf '%s' "$input" | jq -r 'if (.event.kind // "" | endswith("Create")) then "" else .event.oldContent end')"

empty() {
  jq -n '{additionalContext: {change_diff: "", pure_addition: false}}'
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

# Same removed-lines test removal-has-a-grounded-ask.sh ran — recomputed rather
# than passed through, since a prepare step's only input is this same payload.
removed_count="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"
pure_addition=false
[ "${removed_count:-0}" -eq 0 ] && pure_addition=true

# Unified diff, before -> after. diff exits 1 when they differ, so guard it.
change_diff="$(diff -u <(printf '%s' "$old") <(printf '%s' "$new") | tail -n +3 || true)"

jq -n \
  --arg diff "$change_diff" \
  --argjson pure_addition "$pure_addition" \
  '{additionalContext: {change_diff: $diff, pure_addition: $pure_addition}}'
