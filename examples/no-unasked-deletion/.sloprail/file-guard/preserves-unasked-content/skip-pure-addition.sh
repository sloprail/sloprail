#!/usr/bin/env bash
# prepare (the file-guard's copy, reading the settled Post* events at Stop): decide
# whether the judge is asked at all. What it rules on — the
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

# Content by event kind, as removes-content.sh reads it. A Post event's bytes are
# settled; only newContentKnown says whether the engine could read them.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
[ -n "$kind" ] || { echo "preserves-unasked-content: could not read the event's kind, so it could not be checked" >&2; exit 2; }
case "$kind" in
  PostFileCreate|PostFileUpdate)
    # Settled bytes the engine could not read (newContentKnown false): what
    # the change removed is unknown — ask the judge, never skip it.
    [ "$(printf '%s' "$input" | jq -r 'if (.event | has("newContentKnown")) then .event.newContentKnown else true end')" = "true" ] || empty
    new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  PostFileDelete)
    # A PostFileDelete carries the baseline's bytes in oldContent; nothing remains.
    # (oldContentKnown exists only on PreFileDelete — the gate's copy reads it.)
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
