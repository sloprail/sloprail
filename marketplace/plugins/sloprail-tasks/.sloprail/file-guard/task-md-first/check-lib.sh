#!/usr/bin/env bash
# Shared by the task-md-first gate and its file-guard: one library, two thin entries.
# Each entry reads the event kind and its own bytes; nothing that follows branches
# on it.

lib_init() {
set -euo pipefail

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1"; }

kind="$(field '.event.kind // ""')"
path="$(field '.event.path // ""')"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}
}

lib_check() {

[ -n "$path" ] || refuse "the event named no path, so this rule could not check it"

task_dir="$(printf '%s' "$path" | sed -E 's#^(memories/tasks/[^/]+/[^/]+)/.*$#\1#')"
if [ "$task_dir" = "$path" ]; then
  refuse "the event named no path, so this rule could not check it"
fi

if [ ! -f "${SR_WORKSPACE:-.}/$task_dir/TASK.md" ]; then
  refuse "$task_dir has no TASK.md, so it is not a task yet. Write $task_dir/TASK.md first (sr-file write ... --cite:user '<the ask>', per task-body-is-human-authored), then write $path."
fi
exit 0
}

check_lib_loaded=1
