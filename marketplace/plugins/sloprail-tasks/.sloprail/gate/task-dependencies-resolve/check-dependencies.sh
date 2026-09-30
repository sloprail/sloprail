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
# THE PRE-WRITE HALF: this is the gate's copy (pending bytes, `event.newContent`); the
# plain file-guard of the same name carries the Stop copy (settled bytes on disk).
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
lib_dir="$(cd "$(dirname "$0")/../../file-guard/task-dependencies-resolve" && pwd)"
unset check_dependencies_lib_loaded
. "$lib_dir/check-dependencies-lib.sh" || exit 2
[ "${check_dependencies_lib_loaded:-}" = 1 ] || exit 2
lib_init
case "$kind" in
  PreFileCreate|PreFileUpdate)
    if [ "$(printf '%s' "$event" | jq -r '.event.resultKnown // false' 2>/dev/null)" != "true" ]; then
      refuse "task-dependencies-resolve: the result of this write to $path could not be computed ahead of the write (a sed -i, a notebook create, an unresolvable sr-file line), so it could not be checked. Write the file content directly (the Write tool), or run sr-file on its own line."
    fi
    new_content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    ;;
  *)
    exit 0
    ;;
esac
lib_check
