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
set -uo pipefail

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  refuse "task-gates-hold: the event named no path, so there is nothing to check"
fi

root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"

# The schema is the PLUGIN's, read from the plugin's own tree — never a
# consumer-side copy. See task-evidence-resolves/check-task.sh for why.
schema="$gdir/../../schemas/task.cue"

if [ ! -f "$schema" ]; then
  refuse "task-gates-hold: schema not found at $schema — the rule cannot check anything without it."
fi

kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PreFileCreate|PreFileUpdate)
    # The PreFileWrite gate's copy: the pending bytes, off the event. A result the
    # engine could not compute is REFUSED — a task nobody saw is not approved.
    known="$(printf '%s' "$event" | jq -r '.event.resultKnown // false' 2>/dev/null)"
    if [ "$known" != "true" ]; then
      refuse "task-gates-hold: the result of this write to $path could not be computed ahead of the write (a sed -i, a notebook create, an unresolvable sr-file line), so it could not be checked. Write the file content directly (the Write tool), or run sr-file on its own line."
    fi
    new_content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    ;;
  *)
    exit 0
    ;;
esac

new_doc="$(printf '%s' "$new_content" | sr-file validate - --as .md --schema "$schema" --emit 2>/dev/null)"
new_status="$(printf '%s' "$new_doc" | jq -r '.status // empty' 2>/dev/null)"

case "$new_status" in
  to_do|in_progress) : ;;
  *) exit 0 ;;
esac

old_content=""
case "$kind" in
  PreFileUpdate)
    old_content="$(printf '%s' "$event" | jq -r '.event.oldContent // ""' 2>/dev/null)"
    ;;
esac
old_status=""
if [ -n "$old_content" ]; then
  old_doc="$(printf '%s' "$old_content" | sr-file validate - --as .md --schema "$schema" --emit 2>/dev/null)"
  old_status="$(printf '%s' "$old_doc" | jq -r '.status // empty' 2>/dev/null)"
fi

case "$old_status" in
  backlog|blocked|"") : ;;   # "" covers a new task written straight to to_do/in_progress.
  to_do|in_progress|in_review)
    exit 0   # not the backlog/blocked departure moment.
    ;;
esac

# ------------------------------------------------------------- the gates ---
task_dir="$(dirname "$path")"
gates_dir="$root/$task_dir/gates"

if [ ! -d "$gates_dir" ]; then
  # No gates/ directory at all: nothing declared, nothing to hold on. A
  # DELIBERATE fail-open — an absent gates/ is not evidence of an unmet
  # condition, it is the absence of any condition.
  exit 0
fi

problems=""
n=0
while IFS= read -r -d '' g; do
  n=$((n + 1))
  gname="$(basename "$g")"
  # Invoked via `bash "$g"` rather than executing it directly: a gate is a
  # file an AGENT wrote (through an ordinary file write, which never sets the
  # execute bit — unlike a shipped guard script, which the ENGINE requires to
  # be executable because it execs it as a subprocess). Requiring +x here
  # would refuse every gate on the moment it is written, for a reason that has
  # nothing to do with the condition it tests. `bash` runs the file's content
  # regardless of its mode, the same way a `.sh` test fixture is run without
  # needing chmod first.
  out="$(cd "$root" && SR_WORKSPACE="$root" bash "$g" 2>&1)"
  rc=$?
  if [ $rc -ne 0 ]; then
    problems="${problems}  gates/${gname}: FAILED — ${out:-exited $rc with no message}
"
  fi
done < <(find "$gates_dir" -maxdepth 1 -name '*.sh' -type f -print0 2>/dev/null | sort -z)

if [ -n "$problems" ]; then
  refuse "GATES NOT SATISFIED: $path cannot move to $new_status — a deterministic gate failed.

$problems
Fix the underlying condition each gate tests. Do not edit or delete a gate to get past it — a change to a gates/*.sh or gates/*.md file is itself refused unless it stays derived from the task (see task-gate-is-grounded). This SAME set of gates/*.sh files is also re-checked when the task later reaches in_review (task-review) — see run-sh-gates.sh."
fi

exit 0
