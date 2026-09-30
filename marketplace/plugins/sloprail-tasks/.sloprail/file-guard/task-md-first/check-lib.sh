#!/usr/bin/env bash
# Shared by the task-md-first gate and its file-guard: one library, two thin entries.
# The gate entry reads the pending write (lib_init, then a Pre kind); the file-guard
# entry reads the Changeset and calls lib_check once per file. Nothing here reads an
# event: lib_check works on the `path` it is given, against the tree at `lib_root`
# (the project for the gate, the committed head for the file-guard).

lib_setup() {
  set -uo pipefail

  refuse() {
    jq -n --arg r "$1" '{reason: $r}'
    exit 1
  }
}

# lib_init is the gate's: the pending write's own bytes.
lib_init() {
  lib_setup

  payload="$(cat)"
  field() { printf '%s' "$payload" | jq -r "$1"; }

  kind="$(field '.event.kind // ""')"
  path="$(field '.event.path // ""')"
  # The project's working tree: right for the gate ONLY. The file-guard entry never
  # calls lib_init; it sets lib_root to SR_TREE, the committed snapshot.
  lib_root="${SR_WORKSPACE:-}"
  [ -n "$lib_root" ] || refuse "SR_WORKSPACE is not set, so the project's tasks could not be read"
}

lib_check() {
  [ -n "$path" ] || refuse "the event named no path, so this rule could not check it"

  task_dir="$(printf '%s' "$path" | sed -E 's#^(memories/tasks/[^/]+/[^/]+)/.*$#\1#')"
  if [ "$task_dir" = "$path" ]; then
    refuse "the event named no path, so this rule could not check it"
  fi

  if [ ! -f "$lib_root/$task_dir/TASK.md" ]; then
    refuse "$task_dir has no TASK.md, so it is not a task yet. Write $task_dir/TASK.md first (sr-file write ... --cite:user '<the ask>', per task-body-is-human-authored), then write $path."
  fi
  return 0
}

check_lib_loaded=1
