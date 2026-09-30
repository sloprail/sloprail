#!/bin/sh
# The file-guard entry of misplaced-declaration: every .sloprail/ YAML of the
# changeset must sit where the engine reads one, or the refusal says where it
# belongs. A deleted file is not one of them: `deletions:` is left at its default,
# so removing a declaration is never this rule's business. Reads only paths.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset check_path_lib_loaded
. "$lib_dir/check-path-lib.sh" || exit 2
[ "${check_path_lib_loaded:-}" = 1 ] || exit 2
lib_setup

paths="$(jq -r '.changeset.files[].path')" || {
  echo '{"reason":"sloprail/file-guard/misplaced-declaration could not read the changeset on stdin, so it could not check the declarations in it."}'
  exit 1
}

while IFS= read -r path; do
  [ -n "$path" ] || continue
  lib_check
done <<SR_EOF
$paths
SR_EOF
exit 0
