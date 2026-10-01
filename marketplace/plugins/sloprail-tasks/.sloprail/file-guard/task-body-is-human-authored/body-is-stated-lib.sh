#!/usr/bin/env bash
# Shared by the task-body-is-human-authored gate and its file-guard: one library, two thin entries.
# The gate entry reads the pending write (lib_init, then a Pre kind); the file-guard
# entry reads the Changeset and calls lib_check once per file, with `path` and
# `content` set. lib_check reads no event.

lib_setup() {
  set -uo pipefail

  # refuse emits `{"reason": ...}` and exits non-zero, in the CURRENT shell — never
  # behind a pipe (a piped refuse runs in a subshell and its exit would not stop the
  # script, silently PERMITTING).
  refuse() {
    jq -n --arg reason "$1" '{reason: $reason}'
    exit 1
  }

  command -v jq >/dev/null 2>&1 || {
    echo "task-body-is-human-authored could not run: it needs jq, which is not on PATH. Refusing, because a check that could not run has not approved the write." >&2
    exit 1
  }

  lib="$lib_dir/lib-body.sh"
  [ -f "$lib" ] || refuse "task-body-is-human-authored: lib-body.sh not found at $lib, so the body could not be read"
  # A helper stopped by a syntax error runs only up to it (whether the `.` then
  # fails depends on the bash version); only its last-line sentinel proves it
  # loaded whole.
  unset lib_body_loaded
  # shellcheck source=lib-body.sh
  . "$lib"
  [ "${lib_body_loaded:-}" = 1 ] \
    || refuse "task-body-is-human-authored: lib-body.sh did not load whole (its last-line sentinel lib_body_loaded is unset), so the body could not be read"
}

# lib_init is the gate's: the pending write's own bytes.
lib_init() {
  lib_setup

  payload="$(cat)"
  field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

  path="$(field '.event.path // empty')"
  [ -n "$path" ] || refuse "task-body-is-human-authored: the event named no path, so there is nothing to judge"
  kind="$(field '.event.kind // ""')"
}

lib_check() {
  if [ -z "$(task_body "$content" | tr -d '[:space:]')" ]; then
    refuse "TASK BODY IS EMPTY: $path has no body under the frontmatter. A task's body states the human's ask, in the user's own terms — write it, and ground the write in their words (sr-file … --cite:user '<exact words the user wrote>', or a Sloprail-Cites-User: trailer in the commit)."
  fi
  return 0
}

body_is_stated_lib_loaded=1
