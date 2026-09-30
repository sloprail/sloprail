#!/usr/bin/env bash
# `when` for the user citation a removal needs — the AFTER-CHECK half (Post* events,
# at Stop; the gate's copy reads the Pre* ones before the write): does this change
# remove content?
# Exit 0 — it does (a line present before is gone after, or the file is deleted),
# so the change must cite the user's words asking for it. Exit 1 — it only adds,
# so no ask is needed.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 does not permit anything — it
# APPLIES the requirement. So every path this script cannot decide exits 0, the
# fail-closed direction; only exit 1 waives the citation, and only on a decided
# pure addition.
set -uo pipefail

# Undecidable without jq: apply the requirement (exit 0, fail-closed).
command -v jq >/dev/null 2>&1 || exit 0

input="$(cat)"
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
[ -n "$kind" ] || { echo "preserves-unasked-content: could not read the event's kind, so it could not be checked" >&2; exit 2; }
case "$kind" in
  PostFileCreate)
    # A create has nothing before it, so it removes nothing.
    exit 1
    ;;
  PostFileUpdate)
    # Settled bytes the engine could not read — newContentKnown false (declared
    # on PostFileCreate/PostFileUpdate, internal/filemod/module.go): a link to a
    # FIFO or a device, or a file past the read cap. Undecidable: apply (exit 0).
    [ "$(printf '%s' "$input" | jq -r 'if (.event | has("newContentKnown")) then .event.newContentKnown else true end')" = "true" ] || exit 0
    ;;
  *)
    # PostFileDelete: a deletion is the largest removal there is — whether or not the engine
    # read the bytes it loses (oldContentKnown): it always applies.
    exit 0
    ;;
esac

old="$(printf '%s' "$input" | jq -r '.event.oldContent // ""')"
new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"

# Any line present in old but absent in new. (Order/whitespace refinements are
# elided in this sample.)
removed="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"
[ "${removed:-0}" -eq 0 ] && exit 1

# It applies. The hint the refusal carries: append instead, or cite the ask.
jq -n --arg n "$removed" '{hint: (
  "This change removes " + $n + " line(s). If nothing should go, append instead of rewriting; if the user asked for the removal, cite their words asking for it.")}'
exit 0
