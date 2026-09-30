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
set -euo pipefail

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1"; }

kind="$(field '.event.kind // ""')"
path="$(field '.event.path // ""')"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

case "$kind" in
  PostFileCreate | PostFileUpdate) ;;
  *) refuse "unexpected event kind '$kind' for $path; this rule only judges settled file writes" ;;
esac

[ -n "$path" ] || refuse "the event named no path, so this rule could not check it"

task_dir="$(printf '%s' "$path" | sed -E 's#^(memories/tasks/[^/]+/[^/]+)/.*$#\1#')"
if [ "$task_dir" = "$path" ]; then
  refuse "the event named no path, so this rule could not check it"
fi

if [ ! -f "${SR_WORKSPACE:-.}/$task_dir/TASK.md" ]; then
  refuse "$task_dir has no TASK.md, so it is not a task yet. Write $task_dir/TASK.md first (sr-file write ... --cite:user '<the ask>', per task-body-is-human-authored), then write $path."
fi
exit 0
