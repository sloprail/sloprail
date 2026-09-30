#!/usr/bin/env bash
# Stage 1 of task-body-is-human-authored: the DETERMINISTIC half, no model.
#
# A task's body is the human's ask, so a task file must have one: prose under the
# frontmatter. That the write setting it cites the user's words is not this
# script's business — the guard's `require` declares it, conditioned on
# body-changed.sh — and whether the words ground THIS body is stage 2's judge.
#
# THIS IS THE FILE-GUARD ENTRY: it reads each task's committed bytes from the Changeset. The
# PreFileWrite gate of the same name carries the pending-bytes entry.
#
# THE REFUSAL CONTRACT (internal/dispatch/exec.go): exit 0 permits; non-zero
# refuses with `{"reason": "..."}` on stdout. Fails CLOSED on every path it cannot
# decide (no jq, no path, the sibling library missing, an unreadable settled file).
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset body_is_stated_lib_loaded
. "$lib_dir/body-is-stated-lib.sh" || exit 2
[ "${body_is_stated_lib_loaded:-}" = 1 ] || exit 2
lib_setup

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }
[ "$(field '.event.kind // ""')" = "Changeset" ] ||
  refuse "task-body-is-human-authored: expected a Changeset event, so the tasks could not be judged"
# The changeset's files are read through this plugin's one library (a missing
# content field is undecidable, never an empty file).
cs_lib="$(dirname "$0")/../../lib/changeset.sh"
unset changeset_lib_loaded
. "$cs_lib" 2>/dev/null || refuse "task-body-is-human-authored: the changeset library (lib/changeset.sh) could not be loaded, so nothing could be judged"
[ "${changeset_lib_loaded:-}" = 1 ] || refuse "task-body-is-human-authored: the changeset library (lib/changeset.sh) could not be loaded, so nothing could be judged"
file_n="$(cs_count "$payload")" || true
case "$file_n" in '' | *[!0-9]*) refuse "task-body-is-human-authored: the changeset's files could not be read, so nothing could be judged" ;; esac

file_i=0
while [ "$file_i" -lt "$file_n" ]; do
  path="$(cs_get "$payload" "$file_i" .path)" \
    || refuse "task-body-is-human-authored: could not read file $file_i of the changeset"
  content="$(cs_text "$payload" "$file_i" newContent)" \
    || refuse "task-body-is-human-authored: could not read $path from the changeset"
  file_i=$((file_i + 1))
  lib_check
done
exit 0
