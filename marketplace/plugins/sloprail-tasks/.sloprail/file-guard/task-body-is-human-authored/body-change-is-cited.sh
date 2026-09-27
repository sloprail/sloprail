#!/usr/bin/env bash
# Stage 1 of task-body-is-human-authored: the DETERMINISTIC half, no model.
#
# A task's body is the human's ask. When a write CREATES a task, or CHANGES its
# body (the prose after the frontmatter), the write itself must be grounded in
# the user's own words: `.event.citations` must hold at least one citation whose
# sourceTypes include `user`. The citation rides on the ACTION — `sr-file write|
# edit … --cite:user '<exact words>'` — never inside the file, so the task keeps
# only derived text and the source stays checkable. The session resolves every
# quote against its own record before this script runs, so a citation present
# here EXISTS; whether it grounds THIS body is stage 2's judge.
#
# A change that leaves the body byte-identical — a status, a priority, a
# depends_on — needs no citation and is permitted here. Existing bodies that still
# carry old inline `[quote](path)` links are neither required nor refused.
#
# THE TWO MOMENTS. Preventive, so this runs at pre-tool (a Pre kind: the pending
# bytes, compared with `.event.oldContent`, the file on disk) and again at Stop
# (a Post kind: the settled bytes on disk, compared with `.event.oldContent`, the
# session baseline — a create has none). At Stop the event carries every citation
# the path's changes were made with this session, so a grounded body whose
# frontmatter was later edited plainly still carries its ask's citations.
#
# THE REFUSAL CONTRACT (internal/dispatch/exec.go): exit 0 permits; non-zero
# refuses with `{"reason": "..."}` on stdout. Fails CLOSED on every path it cannot
# decide (no jq, no path, the sibling library missing, an unreadable settled file).
set -uo pipefail

# refuse emits `{"reason": ...}` and exits non-zero, in the CURRENT shell — never
# behind a pipe (a piped refuse runs in a subshell and its exit would not stop the
# script, silently PERMITTING). The reason is built into a variable first; its
# dynamic parts are expanded once, never re-evaluated, so a `$(...)` inside a path
# is inert data.
refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

command -v jq >/dev/null 2>&1 || {
  echo "task-body-is-human-authored could not run: it needs jq, which is not on PATH. Refusing, because a check that could not run has not approved the write." >&2
  exit 1
}

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

path="$(field '.event.path // empty')"
[ -n "$path" ] || refuse "task-body-is-human-authored: the event named no path, so there is nothing to judge"
kind="$(field '.event.kind // ""')"

lib="${SR_GUARDRAIL_DIR:-.}/lib-body.sh"
[ -f "$lib" ] || refuse "task-body-is-human-authored: lib-body.sh not found at $lib, so the body could not be read"
# shellcheck source=lib-body.sh
. "$lib"

# WHICH BYTES, and against what. resultKnown is consulted on BOTH Pre kinds before
# newContent is read: an underivable result exits 0 here and is judged on the
# settled bytes at Stop (for a preventive guard the engine refuses such a write
# before this script runs, so this is the script staying correct on its own).
case "$kind" in
  PreFileCreate | PreFileUpdate)
    [ "$(field '.event.resultKnown // false')" = "true" ] || exit 0
    content="$(field '.event.newContent // ""')"
    ;;
  PostFileCreate | PostFileUpdate)
    abs="${SR_WORKSPACE:-.}/$path"
    # Written and removed within the cycle: nothing landed, nothing to judge.
    [ -f "$abs" ] || exit 0
    content="$(cat "$abs")" || refuse "task-body-is-human-authored: could not read $path to judge its body"
    ;;
  *)
    # A delete is not this guard's business (deletions default to skip).
    exit 0
    ;;
esac

body="$(task_body "$content")"

# A status/frontmatter-only change: the body is what it was, so nothing new needs
# grounding. Only an update has prior bytes; a create always needs grounding.
case "$kind" in
  PreFileUpdate | PostFileUpdate)
    old_body="$(task_body "$(field '.event.oldContent // ""')")"
    [ "$body" = "$old_body" ] && exit 0
    what="changes the body of $path"
    ;;
  *)
    what="creates $path"
    ;;
esac

if [ -z "$(printf '%s' "$body" | tr -d '[:space:]')" ]; then
  refuse "TASK BODY IS EMPTY: this write $what with no body under the frontmatter. A task's body states the human's ask, in the user's own terms — write it, and ground the write in their words with sr-file … --cite:user '<exact words the user wrote>'."
fi

cites="$(user_citations "$payload")"
n="$(printf '%s' "${cites:-[]}" | jq 'length' 2>/dev/null)"
[ "${n:-0}" -gt 0 ] && exit 0

IFS= read -r -d '' how <<'EOF' || true
A task's body is the human's ask, and the write that sets it must be grounded in
the user's own words. The citation rides on the command, never in the file: make
the change with sr-file, quoting the user exactly, and run it ON ITS OWN in the
Bash line (nothing else on the line but sr-file calls, &&, and echo), so its
result can be checked before it lands:

EOF
IFS= read -r -d '' rules <<'EOF' || true

Single-quote the quote; it must match exactly one message the user wrote in this
session — check one with `sr-session trajectory cite '<quote>'`. A paraphrase,
your own words, or a tool's output do not resolve. --cite:user is repeatable:
cite every message the body draws on. The body itself needs no links; state the
ask in the user's terms. A change to the frontmatter alone (status, priority,
depends_on) needs no citation.
EOF

forms="  sr-file write $path --cite:user '<exact words the user wrote>' <<'TASK'
  ---
  status: to_do
  priority: P1
  ---

  <the ask, in the user's terms>
  TASK

  sr-file edit $path --old-string '<old body text>' --new-string '<new body text>' --cite:user '<exact words the user wrote>'
"

case "$kind" in
  Pre*)
    refuse "TASK BODY IS NOT GROUNDED: this write $what, and it carries no citation of the user's own words.
$how$forms$rules"
    ;;
  *)
    refuse "TASK BODY IS NOT GROUNDED: $path was created or had its body changed this session, and no citation of the user's own words is on record for the body it holds now — it was written without one. Rewrite it with sr-file as it should stand, citing the words it rests on.
$how$forms$rules"
    ;;
esac
