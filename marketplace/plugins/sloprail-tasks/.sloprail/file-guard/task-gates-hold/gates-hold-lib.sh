#!/usr/bin/env bash
# Shared by the task-gates-hold gate and its file-guard: one library, two thin entries.
# The gate entry reads the pending write (lib_init, then a Pre kind); the file-guard
# entry reads the Changeset and calls lib_check once per task that is making the
# transition, with `path`, `old_content`, `new_status` and `root` (the committed head)
# set. lib_check reads no event.

lib_setup() {
  set -uo pipefail

  refuse() {
    jq -n --arg reason "$1" '{reason: $reason}'
    exit 1
  }

  gdir="${SR_GUARDRAIL_DIR:-.}"

  # The schema is the PLUGIN's, read from the plugin's own tree — never a
  # consumer-side copy. See task-evidence-resolves/check-task.sh for why.
  schema="$gdir/../../schemas/task.cue"

  if [ ! -f "$schema" ]; then
    refuse "task-gates-hold: schema not found at $schema — the rule cannot check anything without it."
  fi
}

# lib_init is the gate's: the pending write's own bytes.
lib_init() {
  lib_setup

  event="$(cat)"

  path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
  if [ -z "$path" ]; then
    refuse "task-gates-hold: the event named no path, so there is nothing to check"
  fi

  # The tree the task's gates are read from and run in: the project for the gate.
  root="${SR_WORKSPACE:-.}"

  kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)" || refuse "task-gates-hold: could not read the event's kind, so the task could not be checked"
  [ -n "$kind" ] || refuse "task-gates-hold: the event named no kind, so the task could not be checked"
}

lib_check() {
old_status=""
if [ -n "$old_content" ]; then
  old_doc="$(printf '%s' "$old_content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)" || refuse "task-gates-hold: sr-file could not validate the previous $path: $old_doc"
  old_status="$(printf '%s' "$old_doc" | jq -r '.status // empty' 2>/dev/null)"
fi

case "$old_status" in
  backlog|blocked|"") : ;;   # "" covers a new task written straight to to_do/in_progress.
  to_do|in_progress|in_review)
    return 0   # not the backlog/blocked departure moment.
    ;;
esac

# ------------------------------------------------------------- the gates ---
task_dir="$(dirname "$path")"
gates_dir="$root/$task_dir/gates"

if [ ! -d "$gates_dir" ]; then
  # No gates/ directory at all: nothing declared, nothing to hold on. A
  # DELIBERATE fail-open — an absent gates/ is not evidence of an unmet
  # condition, it is the absence of any condition.
  return 0
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
Fix the underlying condition each gate tests. Do not edit or delete a gate to get past it — a change to a gates/*.sh or gates/*.md file is itself refused unless it stays derived from the task (see task-gate-is-grounded). This SAME set of gates/*.sh files is also re-checked when the task later reaches in_review (task-review)."
fi

return 0
}

gates_hold_lib_loaded=1
