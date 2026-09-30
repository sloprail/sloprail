#!/bin/sh
# The gate entry of misplaced-declaration: the pending write's path. The rule reads
# only the path, so the check is the library's, shared with the file-guard.
lib_dir="$(cd "$(dirname "$0")/../../file-guard/misplaced-declaration" && pwd)"
unset check_path_lib_loaded
. "$lib_dir/check-path-lib.sh" || exit 2
[ "${check_path_lib_loaded:-}" = 1 ] || exit 2
lib_setup

path="$(jq -r '.event.path // ""')" || {
  echo '{"reason":"sloprail/gate/misplaced-declaration could not read the event on stdin, so it could not check the declaration."}'
  exit 1
}
[ -n "$path" ] || {
  echo '{"reason":"sloprail/gate/misplaced-declaration: the event named no path, so it could not check the declaration."}'
  exit 1
}
lib_check
