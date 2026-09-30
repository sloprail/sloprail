#!/usr/bin/env bash
# A task's `depends_on` — other tasks that must be DONE first — the
# DETERMINISTIC gate on entering to_do/in_progress/in_review.
#
# TWO questions, both deterministic, no model:
#
#   1. UNFINISHED OR UNKNOWN DEPENDENCY: for every id in depends_on, does
#      memories/tasks/<id>/TASK.md exist? Tasks are DELETED when the reviewer
#      approves them (the plugin's own lifecycle — see README), so "the
#      folder is still there" means "not done", and this is the ONLY
#      question asked about existence — there is deliberately no separate
#      typo/git-history distinction here. A dependency id that never named a
#      real task and one that named a real, now-finished task are
#      INDISTINGUISHABLE from a live folder scan alone, and this guard does
#      not need to tell them apart: either way, the id currently resolves to
#      nothing, so the move is refused with the same message either way ("no
#      task there — if it is done, remove the dependency; if it was a typo,
#      fix it"). What used to need a git-history check to rule out a
#      dangling-reference false-negative is now covered by a SEPARATE
#      invariant: no-dangling-dependencies-at-turn-end (a Stop gate) refuses
#      to let a turn end if ANY task's depends_on names a folder that does
#      not exist — so a depends_on id can never legitimately point at
#      "used to exist, now done" while looking identical to "never existed";
#      deleting a task and leaving a dangling reference to it is refused
#      before this guard would ever need to guess which case it is looking
#      at.
#   2. CYCLES: build the depends_on graph from every TASK.md on disk (plus
#      this pending write's own edges), and refuse if this task's id is
#      reachable from itself.
#
# THE FILE-GUARD ENTRY: it checks every task in the changeset, and reads the task
# tree from SR_TREE (the committed head), so a half-finished edit in the working
# tree is never what a dependency is resolved against. The PreFileWrite gate of the
# same name carries the pending-bytes entry.
#
# THE GATE: like task-gates-hold, this only matters on entering
# to_do/in_progress/in_review — moving among backlog/blocked, or staying
# in_progress, costs nothing. Unlike gates (checked only on the
# backlog/blocked departure), depends_on additionally covers in_review: a task
# cannot even CLAIM to be finished while a dependency is unfinished.
#
# THE REFUSAL CONTRACT: exit 0 permits; non-zero refuses with
# `{"reason": "..."}` on stdout. Fails CLOSED: an id this script cannot
# resolve (an unreadable tree) is treated as unresolved, i.e. refused, never
# silently skipped.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset check_dependencies_lib_loaded
. "$lib_dir/check-dependencies-lib.sh" || exit 2
[ "${check_dependencies_lib_loaded:-}" = 1 ] || exit 2
lib_setup

event="$(cat)"
[ "$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)" = "Changeset" ] ||
  refuse "task-dependencies-resolve: expected a Changeset event, so the tasks could not be checked"
root="${SR_TREE:-}"
[ -n "$root" ] || refuse "task-dependencies-resolve: SR_TREE is not set, so the committed tasks could not be read"

# The changeset's files are read through this plugin's one library (a missing
# content field is undecidable, never an empty file).
cs_lib="$(cd "$(dirname "$0")" && pwd)/../../lib/changeset.sh"
unset changeset_lib_loaded
. "$cs_lib" 2>/dev/null || refuse "task-dependencies-resolve: the changeset library (lib/changeset.sh) could not be loaded, so the tasks could not be checked"
[ "${changeset_lib_loaded:-}" = 1 ] || refuse "task-dependencies-resolve: the changeset library (lib/changeset.sh) could not be loaded, so the tasks could not be checked"
file_n="$(cs_count "$event")" ||
  refuse "task-dependencies-resolve: the changeset's files could not be read, so the tasks could not be checked"
case "$file_n" in '' | *[!0-9]*) refuse "task-dependencies-resolve: the changeset's files could not be read, so the tasks could not be checked" ;; esac

file_i=0
while [ "$file_i" -lt "$file_n" ]; do
  path="$(cs_get "$event" "$file_i" .path)" ||
    refuse "task-dependencies-resolve: could not read file $file_i of the changeset"
  new_content="$(cs_text "$event" "$file_i" newContent)" ||
    refuse "task-dependencies-resolve: could not read $path from the changeset"
  file_i=$((file_i + 1))
  # An emptied task has no dependencies to resolve.
  [ -n "$new_content" ] || continue
  lib_check
done
exit 0
