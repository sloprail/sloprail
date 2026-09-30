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
file_n="$(field '.changeset.files | length')" || true
case "$file_n" in '' | *[!0-9]*) refuse "task-body-is-human-authored: the changeset's files could not be read, so nothing could be judged" ;; esac

file_i=0
while [ "$file_i" -lt "$file_n" ]; do
  path="$(printf '%s' "$payload" | jq -r --argjson i "$file_i" '.changeset.files[$i].path')" \
    || refuse "task-body-is-human-authored: could not read file $file_i of the changeset"
  content="$(printf '%s' "$payload" | jq -r --argjson i "$file_i" '.changeset.files[$i].newContent')" \
    || refuse "task-body-is-human-authored: could not read $path from the changeset"
  file_i=$((file_i + 1))
  lib_check
done
exit 0
