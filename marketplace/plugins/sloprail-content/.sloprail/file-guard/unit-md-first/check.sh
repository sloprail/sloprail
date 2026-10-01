#!/usr/bin/env bash
# The file-guard entry of unit-md-first: refuse a committed file inside a unit folder
# whose UNIT.md is not there. The gate of the same name refuses the pending write;
# this judges the commits, so it catches what only lands past the gate (a shell
# write whose result the gate could not see). Path-based, and read against SR_TREE,
# the committed head, so a UNIT.md still uncommitted does not count.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset check_lib_loaded
. "$lib_dir/check-lib.sh" || exit 2
[ "${check_lib_loaded:-}" = 1 ] || exit 2
lib_setup

payload="$(cat)"
[ "$(printf '%s' "$payload" | jq -r '.event.kind // ""')" = "Changeset" ] ||
  refuse "unexpected event kind, so this rule could not check the files; it only judges a changeset"
[ -n "${SR_TREE:-}" ] || refuse "SR_TREE is not set, so the committed units could not be read"
paths="$(printf '%s' "$payload" | jq -r '.changeset.files[].path')" ||
  refuse "the changeset could not be read, so this rule could not check the files"

lib_root="$SR_TREE"
while IFS= read -r path; do
  [ -n "$path" ] || continue
  lib_check
done <<<"$paths"
exit 0
