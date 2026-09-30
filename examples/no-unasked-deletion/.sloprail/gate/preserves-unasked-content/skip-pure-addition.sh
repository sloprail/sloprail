#!/usr/bin/env bash
# prepare (the gate's copy, reading Pre* events): decide whether the judge is asked
# at all. What it rules on — the
# change's unified diff and the cited words — needs no preparing: the template
# reads `change` and `.event.citations` straight off its input.
#
# SKIPS THE JUDGE on a PURE ADDITION — REQUIRED, not cosmetic. When
# removes-content.sh waives the citation for an append, this judge would still
# run and judge a diff with nothing removed against no citation (correctly none:
# nothing needed authorizing), and refuse a healthy append. Measured against a
# real Haiku run (eval/add-section). `{"skip": true}` abstains instead, and no
# model call is spent on a change that removes nothing.
set -uo pipefail

input="$(cat)"
# oldContent exists only on the update and delete kinds; a create has nothing
# before it, so its prior content is empty by construction, not by default.
old="$(printf '%s' "$input" | jq -r 'if (.event.kind // "" | endswith("Create")) then "" else .event.oldContent end')"

empty() {
  jq -n '{additionalContext: {}}'
  exit 0
}

# Content by event kind, as removes-content.sh reads it. resultKnown is declared
# on the Pre create/update kinds: a result the engine could not compute hands the
# judge an empty context (require-known-result.sh has refused it before this).
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
[ -n "$kind" ] || { echo "preserves-unasked-content: could not read the event's kind, so it could not be checked" >&2; exit 2; }
case "$kind" in
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    [ "$known" = "true" ] || empty
    new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  PreFileDelete)
    # A delete whose bytes the engine did not read (oldContentKnown false: past
    # a recursive removal's read budget, larger than a delete read, or not a
    # regular file) loses content nobody can see — never "nothing removed". An
    # empty oldContent there used to skip the judge, and any resolvable quote
    # of the user's then admitted the delete. Ask the judge.
    [ "$(printf '%s' "$input" | jq -r 'if (.event | has("oldContentKnown")) then .event.oldContentKnown else true end')" = "true" ] || empty
    new=""
    ;;
  *)
    empty
    ;;
esac

# Same removed-lines test removes-content.sh ran — recomputed rather
# than passed through, since a prepare step's only input is this same payload.
removed_count="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"
if [ "${removed_count:-0}" -eq 0 ]; then
  printf '{"skip": true}\n'
  exit 0
fi

jq -n '{additionalContext: {}}'
