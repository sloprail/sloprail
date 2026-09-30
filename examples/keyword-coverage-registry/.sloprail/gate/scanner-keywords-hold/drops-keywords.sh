#!/usr/bin/env bash
# `when` for the user citation a scanner's keywords need: does this write DROP a
# keyword the scanner already declared? Exit 0 — it does, so the write must cite
# the user's words asking for it. Exit 1 — it does not (a new scanner, or one that
# only adds keywords), so no citation is required. Deleting the scanner drops
# every keyword it declared, so a delete of one that declared any applies too.
# This is the PRE-WRITE copy (the gate); the file-guard of the same name keeps the
# Stop-time copy for the settled file, where a create can drop keywords too.
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
lib_dir="$(cd "$(dirname "$0")/../../file-guard/scanner-keywords-hold" && pwd)"
unset drops_keywords_lib_loaded
. "$lib_dir/drops-keywords-lib.sh" || exit 2
[ "${drops_keywords_lib_loaded:-}" = 1 ] || exit 2
lib_init
case "$kind" in
  PreFileCreate)
    # A result the engine could not compute is undecidable: apply (exit 0).
    [ "$(field '.event.resultKnown // false')" = "true" ] || exit 0
    old=""
    new="$(field '.event.newContent // ""')"
    ;;
  PreFileUpdate)
    [ "$(field '.event.resultKnown // false')" = "true" ] || exit 0
    old="$(field '.event.oldContent // ""')"
    new="$(field '.event.newContent // ""')"
    ;;
  PreFileDelete)
    # Deleting a scanner drops EVERY keyword it declared — measured on a real
    # run: refused by the coverage gate, a sub-agent ran `rm -rf scanners/<name>`
    # instead of searching. Nothing remains, so the new side is empty.
    old="$(field '.event.oldContent // ""')"
    # Bytes the engine did not read (a file past a removal's byte budget, or not
    # a regular file) and nothing owed to fall back on: undecidable, apply.
    if [ "$(field 'if .event | has("oldContentKnown") then .event.oldContentKnown else true end')" != "true" ] && [ -z "$owed" ]; then
      exit 0
    fi
    lib_check_delete
    exit $?
    ;;
  *)
    # A kind this script does not know: undecidable, so apply (fail-closed).
    exit 0
    ;;
esac
lib_check
