#!/usr/bin/env bash
# The file-guard entry of task-md-first: refuse a committed file inside a task folder
# whose TASK.md is not there. Path-based, and read against SR_TREE, the committed
# head, so a TASK.md still uncommitted does not count. The gate of the same name
# refuses the pending write; this judges the commits.
#
# The task folder is the FIRST TWO segments after memories/tasks/, not simply
# dirname(path): a gates/*.sh or gates/*.md file sits one level deeper than
# TASK.md itself (memories/tasks/<group>/<task>/gates/<name>), so dirname
# would look for gates/TASK.md, which is never where it lives (check-lib.sh).
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset check_lib_loaded
. "$lib_dir/check-lib.sh" || exit 2
[ "${check_lib_loaded:-}" = 1 ] || exit 2
lib_setup

payload="$(cat)"
[ "$(printf '%s' "$payload" | jq -r '.event.kind // ""')" = "Changeset" ] ||
  refuse "unexpected event kind, so this rule could not check the files; it only judges a changeset"
[ -n "${SR_TREE:-}" ] || refuse "SR_TREE is not set, so the committed tasks could not be read"
paths="$(printf '%s' "$payload" | jq -r '.changeset.files[].path')" ||
  refuse "the changeset could not be read, so this rule could not check the files"

lib_root="$SR_TREE"
while IFS= read -r path; do
  [ -n "$path" ] || continue
  lib_check
done <<<"$paths"
exit 0
