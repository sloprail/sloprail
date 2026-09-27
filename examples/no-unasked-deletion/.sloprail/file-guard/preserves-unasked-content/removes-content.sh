#!/usr/bin/env bash
# `when` for the user citation a removal needs: does this change remove content?
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
case "$kind" in
  PreFileCreate|PostFileCreate)
    # A create has nothing before it, so it removes nothing.
    exit 1
    ;;
  PreFileUpdate)
    # A result the engine could not compute is undecidable: apply (exit 0).
    # resultKnown is declared only on the Pre kinds; a Post event's bytes are
    # settled.
    [ "$(printf '%s' "$input" | jq -r '.event.resultKnown // false')" = "true" ] || exit 0
    ;;
  PostFileUpdate)
    ;;
  *)
    # A deletion is the largest removal there is — whether or not the engine
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
