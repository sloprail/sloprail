#!/usr/bin/env bash
# `when` for the user citation a removal needs — the PRE-WRITE half (Pre* events;
# the file-guard's copy reads the settled Post* ones): does this change remove
# content?
# Exit 0 — it does (a line present before is gone after, or the file is deleted),
# so the change must cite the user's words asking for it. Exit 1 — it only adds,
# so no ask is needed.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 does not permit anything — it
# APPLIES the requirement. So every path this script cannot decide exits 0, the
# fail-closed direction; only exit 1 waives the citation, and only on a decided
# pure addition.
lib_dir="$(cd "$(dirname "$0")/../../file-guard/preserves-unasked-content" && pwd)"
unset removes_content_lib_loaded
. "$lib_dir/removes-content-lib.sh" || exit 2
[ "${removes_content_lib_loaded:-}" = 1 ] || exit 2
lib_init
case "$kind" in
  PreFileCreate)
    # A create has nothing before it, so it removes nothing.
    exit 1
    ;;
  PreFileUpdate)
    # A result the engine could not compute (`sed -i`, `>`, an sr-file line it
    # could not resolve) is undecidable: apply (exit 0).
    [ "$(printf '%s' "$input" | jq -r '.event.resultKnown // false')" = "true" ] || exit 0
    ;;
  *)
    # PreFileDelete: a deletion is the largest removal there is — whether or not the engine
    # read the bytes it loses (oldContentKnown): it always applies.
    exit 0
    ;;
esac
lib_check
