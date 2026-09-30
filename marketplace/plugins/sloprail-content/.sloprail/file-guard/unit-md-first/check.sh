#!/usr/bin/env bash
# Stop after-check of unit-md-first: refuse a SETTLED file inside a unit folder
# whose UNIT.md does not exist. The gate of the same name refuses the pending
# write; this catches what only lands past it (a shell write whose result the
# gate could not see). Path-based, not content-based.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset check_lib_loaded
unset check_lib_loaded
. "$lib_dir/check-lib.sh" || exit 2
[ "${check_lib_loaded:-}" = 1 ] || exit 2
[ "${check_lib_loaded:-}" = 1 ] || exit 2
lib_init
case "$kind" in
  PostFileCreate | PostFileUpdate) ;;
  *) refuse "unexpected event kind '$kind' for $path; this rule only judges settled file writes" ;;
esac
lib_check
