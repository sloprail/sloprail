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
# transition OUT of backlog/blocked INTO to_do/in_progress — moving among
# backlog/blocked, staying in to_do/in_progress, or moving to in_review costs
# nothing here (in_review's own claim is task-review's subject, over
# DELIVERY evidence, not start conditions).
#
# EVERY .sh FILE under this task's gates/ is run, in NAME order (so a run is
# reproducible and a refusal always names the same first failure), with
# SR_WORKSPACE set and the task's own directory as its cwd. A gate that is not
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
lib_init
case "$kind" in
  PostFileCreate|PostFileUpdate)
    # The engine declares newContentKnown on PostFileCreate and PostFileUpdate
    # (internal/filemod/module.go FieldNewContentKnown; authoring-guardrails/
    # events.md): false when it could not read the settled file — a link to a
    # FIFO or a device, or past the read cap. Unseen: refuse, not pass.
    if [ "$(printf '%s' "$event" | jq -r '.event.newContentKnown // false' 2>/dev/null)" != "true" ]; then
      refuse "task-gates-hold: $path could not be read (not a regular file, or too large), so its gates could not be checked"
    fi
    abs="$root/$path"
    if [ ! -f "$abs" ]; then
      exit 0
    fi
    new_content="$(cat "$abs")" || refuse "task-gates-hold: could not read $path"
    if [ ! -s "$abs" ]; then
      exit 0
    fi
    ;;
  *)
    exit 0
    ;;
esac

new_doc="$(printf '%s' "$new_content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)" || refuse "task-gates-hold: sr-file could not validate $path, so its status is unknown: $new_doc"
new_status="$(printf '%s' "$new_doc" | jq -r '.status // empty' 2>/dev/null)"
[ -n "$new_status" ] || refuse "task-gates-hold: $path carries no readable status, so its gates could not be checked"

case "$new_status" in
  to_do|in_progress) : ;;
  *) exit 0 ;;
esac

old_content=""
case "$kind" in
  PostFileUpdate)
    old_content="$(printf '%s' "$event" | jq -r '.event.oldContent // ""' 2>/dev/null)"
    ;;
esac
lib_check
