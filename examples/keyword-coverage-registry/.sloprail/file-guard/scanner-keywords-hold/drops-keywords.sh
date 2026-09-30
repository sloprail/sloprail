#!/usr/bin/env bash
# `when` for the user citation a scanner's keywords need: does this write DROP a
# keyword the scanner already declared? Exit 0 — it does, so the write must cite
# the user's words asking for it. Exit 1 — it does not (a new scanner, or one that
# only adds keywords), so no citation is required. Deleting the scanner drops
# every keyword it declared, so a delete of one that declared any applies too.
# A create can drop keywords too: the settled file of a scanner declared this
# session reaches Stop as a PostFileCreate, and it drops whatever the registry
# owes that the file no longer declares.
#
# This is the STOP-TIME copy (the file-guard): it reads settled bytes, guarded by
# `newContentKnown`. A file-guard never sees a Pre event, so the Pre-only
# `resultKnown` field does not apply here; the gate of the same name keeps the
# pre-write copy.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 does not permit anything — it
# APPLIES the requirement. So every path this script cannot decide exits 0, the
# fail-closed direction; only exit 1 waives the citation, and only on a decided
# "drops nothing".
#
# The failure it exists for, measured on a real Haiku run: refused by the
# coverage gate for a search that missed keywords, the agent rewrote the
# scanner's keywords to fit the search it had already run — the declaration
# weakened to match the work, instead of the work meeting the declaration.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset drops_keywords_lib_loaded
. "$lib_dir/drops-keywords-lib.sh" || exit 2
[ "${drops_keywords_lib_loaded:-}" = 1 ] || exit 2
lib_init
case "$kind" in
  PostFileCreate | PostFileUpdate)
    # Settled bytes the engine could not read (newContentKnown false):
    # undecidable, apply.
    [ "$(field 'if (.event | has("newContentKnown")) then .event.newContentKnown else true end')" = "true" ] || exit 0
    # oldContent exists on PostFileUpdate only: a create has nothing before it.
    case "$kind" in
      PostFileCreate) old="" ;;
      *) old="$(field '.event.oldContent // ""')" ;;
    esac
    new="$(field '.event.newContent // ""')"
    ;;
  PostFileDelete)
    # Deleting a scanner drops EVERY keyword it declared — measured on a real
    # run: refused by the coverage gate, a sub-agent ran `rm -rf scanners/<name>`
    # instead of searching. Nothing remains, so the new side is empty.
    old="$(field '.event.oldContent // ""')"
    # A PostFileDelete carries the baseline's bytes in oldContent (oldContentKnown
    # exists only on PreFileDelete, which the gate's copy handles).
    dropped="$( { keywords_of "$old"; printf '%s\n' "$owed"; } | sed '/^$/d' | sort -u | paste -sd ',' -)"
    # A scanner that declares no keyword, and owes none, drops none.
    [ -n "$dropped" ] || exit 1
    jq -n --arg dropped "$dropped" '{hint: (
      "Deleting this scanner drops every keyword it declared (" + $dropped + "). A scanner declared this session stays owed a search covering all its keywords even once its file is gone (verify-scanner-coverage reads what was logged, not the file), so cover them in one gh search instead. " +
      "Delete a scanner only if the user asked for it, citing their words.")}'
    exit 0
    ;;
  *)
    # A kind this script does not know: undecidable, so apply (fail-closed).
    exit 0
    ;;
esac
lib_check
