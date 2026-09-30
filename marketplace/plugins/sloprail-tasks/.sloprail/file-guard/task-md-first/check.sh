#!/usr/bin/env bash
# Refuse a write inside a task folder whose TASK.md does not exist yet.
# Path-based, not content-based, so an unresolvable pre-write result is still
# judged. Shared by the gate (pending writes, refused before they land) and the
# plain file-guard (settled writes, at Stop): each folder carries its own copy.
#
# The task folder is the FIRST TWO segments after memories/tasks/, not simply
# dirname(path): a gates/*.sh or gates/*.md file sits one level deeper than
# TASK.md itself (memories/tasks/<group>/<task>/gates/<name>), so dirname
# would look for gates/TASK.md, which is never where it lives.
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
