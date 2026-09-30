#!/usr/bin/env bash
# Stage 1 of task-gates-hold: the DETERMINISTIC half — every `.sh` gate.
#
# A task's start conditions are FILES, not frontmatter:
#   memories/tasks/<group>/<name>/gates/<gate-name>.sh   a deterministic
#                                                          condition — exit 0
#                                                          passes, non-zero
#                                                          fails (with a
#                                                          reason on stdout or
#                                                          stderr)
#   memories/tasks/<group>/<name>/gates/<gate-name>.md   a judgment condition
#                                                          — a judge prompt,
#                                                          stage 2's subject
#
# THE GATE: like task-dependencies-resolve, gates are checked ONLY on the
# transition OUT of backlog/blocked INTO to_do/in_progress (a task's status at the
# range's base against its status at head; a task added by the range has none
# before) — moving among
# backlog/blocked, staying in to_do/in_progress, or moving to in_review costs
# nothing here (in_review's own claim is task-review's subject, over
# DELIVERY evidence, not start conditions).
#
# EVERY .sh FILE under this task's gates/ is run, in NAME order (so a run is
# reproducible and a refusal always names the same first failure), in the committed
# tree (SR_TREE, the file-guard entry's root) with SR_WORKSPACE pointed at it. A gate that is not
# executable, or that cannot be run at all, is a REFUSAL (fail-closed) — a
# gate this rule could not actually run is not evidence the condition holds.
# `.md` files are skipped here entirely; they are stage 2's subject.
#
# WHY THIS DOES NOT ALSO VET WHAT A .sh GATE CHECKS. This script runs every
# gate faithfully; it makes NO judgement about whether a gate is a meaningful
# test of anything (a bare `exit 0` gate would pass here). That is
# task-gate-is-grounded's job, at WRITE time: a gate that tests nothing
# traceable to the task itself is refused before it ever lands, so a
# trivial gate never reaches this script to be faithfully "passed". This
# script trusts that every gate under gates/ already cleared that bar.
#
# THE REFUSAL CONTRACT: exit 0 permits; non-zero refuses with
# `{"reason": "..."}` on stdout. Fails CLOSED on both the transition logic and
# a gate that cannot be run; fails OPEN only on genuinely absent plumbing
# (no gates/ directory at all — nothing to hold on), documented inline.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset gates_hold_lib_loaded
. "$lib_dir/gates-hold-lib.sh" || exit 2
[ "${gates_hold_lib_loaded:-}" = 1 ] || exit 2
lib_setup

event="$(cat)"
[ "$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)" = "Changeset" ] ||
  refuse "task-gates-hold: expected a Changeset event, so the tasks could not be checked"
root="${SR_TREE:-}"
[ -n "$root" ] || refuse "task-gates-hold: SR_TREE is not set, so the committed tasks could not be read"

file_n="$(printf '%s' "$event" | jq -r '.changeset.files | length')" || true
case "$file_n" in '' | *[!0-9]*) refuse "task-gates-hold: the changeset's files could not be read, so the tasks could not be checked" ;; esac

file_i=0
while [ "$file_i" -lt "$file_n" ]; do
  path="$(printf '%s' "$event" | jq -r --argjson i "$file_i" '.changeset.files[$i].path')" ||
    refuse "task-gates-hold: could not read file $file_i of the changeset"
  status="$(printf '%s' "$event" | jq -r --argjson i "$file_i" '.changeset.files[$i].status')" ||
    refuse "task-gates-hold: could not read $path from the changeset"
  new_content="$(printf '%s' "$event" | jq -r --argjson i "$file_i" '.changeset.files[$i].newContent | if type == "string" then . else error("missing newContent") end')" ||
    refuse "task-gates-hold: could not read $path from the changeset"
  old_content="$(printf '%s' "$event" | jq -r --argjson i "$file_i" '.changeset.files[$i].oldContent | if type == "string" then . else error("missing oldContent") end')" ||
    refuse "task-gates-hold: could not read the earlier $path from the changeset"
  file_i=$((file_i + 1))
  [ -n "$new_content" ] || continue
  # A task the range added had no status before ("" covers a new task written
  # straight to to_do/in_progress).
  [ "$status" = "A" ] && old_content=""

  new_doc="$(printf '%s' "$new_content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)" || refuse "task-gates-hold: sr-file could not validate $path, so its status is unknown: $new_doc"
  new_status="$(printf '%s' "$new_doc" | jq -r '.status // empty' 2>/dev/null)"
  [ -n "$new_status" ] || refuse "task-gates-hold: $path carries no readable status, so its gates could not be checked"

  case "$new_status" in
    to_do|in_progress) : ;;
    *) continue ;;
  esac

  lib_check
done
exit 0
