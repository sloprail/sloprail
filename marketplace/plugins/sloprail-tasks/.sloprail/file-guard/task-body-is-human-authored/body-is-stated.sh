#!/usr/bin/env bash
# Stage 1 of task-body-is-human-authored: the DETERMINISTIC half, no model.
#
# A task's body is the human's ask, so a task file must have one: prose under the
# frontmatter. That the write setting it cites the user's words is not this
# script's business — the guard's `require` declares it, conditioned on
# body-changed.sh — and whether the words ground THIS body is stage 2's judge.
#
# THIS IS THE STOP (file-guard) COPY: it reads the settled bytes. The PreFileWrite gate
# of the same name carries the pending-bytes copy.
#
# THE REFUSAL CONTRACT (internal/dispatch/exec.go): exit 0 permits; non-zero
# refuses with `{"reason": "..."}` on stdout. Fails CLOSED on every path it cannot
# decide (no jq, no path, the sibling library missing, an unreadable settled file).
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

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

path="$(field '.event.path // empty')"
[ -n "$path" ] || refuse "task-body-is-human-authored: the event named no path, so there is nothing to judge"
kind="$(field '.event.kind // ""')"

lib="${SR_GUARDRAIL_DIR:-.}/lib-body.sh"
[ -f "$lib" ] || refuse "task-body-is-human-authored: lib-body.sh not found at $lib, so the body could not be read"
# A helper stopped by a syntax error runs only up to it (whether the `.` then
# fails depends on the bash version); only its last-line sentinel proves it
# loaded whole.
unset lib_body_loaded
# shellcheck source=lib-body.sh
. "$lib"
[ "${lib_body_loaded:-}" = 1 ] \
  || refuse "task-body-is-human-authored: lib-body.sh did not load whole (its last-line sentinel lib_body_loaded is unset), so the body could not be read"

# WHICH BYTES. The settled file on disk.
case "$kind" in
  PostFileCreate | PostFileUpdate)
    # The engine declares newContentKnown on PostFileCreate and PostFileUpdate
    # (internal/filemod/module.go FieldNewContentKnown; authoring-guardrails/
    # events.md): false when it could not read the settled file — a link to a
    # FIFO or a device, or past the read cap. Unseen: refuse, not pass.
    [ "$(field '.event.newContentKnown // false')" = "true" ] ||
      refuse "task-body-is-human-authored: $path could not be read (not a regular file, or too large), so its body could not be judged"
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

if [ -z "$(task_body "$content" | tr -d '[:space:]')" ]; then
  refuse "TASK BODY IS EMPTY: $path has no body under the frontmatter. A task's body states the human's ask, in the user's own terms — write it, and ground the write in their words with sr-file … --cite:user '<exact words the user wrote>'."
fi
exit 0
