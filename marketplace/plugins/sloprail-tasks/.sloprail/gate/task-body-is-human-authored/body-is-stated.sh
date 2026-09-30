#!/usr/bin/env bash
# Stage 1 of task-body-is-human-authored: the DETERMINISTIC half, no model.
#
# A task's body is the human's ask, so a task file must have one: prose under the
# frontmatter. That the write setting it cites the user's words is not this
# script's business — the guard's `require` declares it, conditioned on
# body-changed.sh — and whether the words ground THIS body is stage 2's judge.
#
# THIS IS THE PRE-WRITE (gate) COPY: it reads the pending bytes off the event. The plain
# file-guard of the same name carries the Stop copy over the settled bytes.
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

# WHICH BYTES. The pending bytes, off the event. resultKnown false means the engine
# could not compute them (a sed -i, a notebook create, an unresolvable sr-file
# line): refuse — a body nobody saw cannot be judged.
case "$kind" in
  PreFileCreate | PreFileUpdate)
    if [ "$(field '.event.resultKnown // false')" != "true" ]; then
      refuse "task-body-is-human-authored: the result of this write to $path could not be computed ahead of the write (a sed -i, a notebook create, an unresolvable sr-file line), so it could not be checked. Write the file content directly (the Write tool), or run sr-file on its own line."
    fi
    content="$(field '.event.newContent // ""')"
    ;;
  *)
    # A delete is not this gate's business.
    exit 0
    ;;
esac

if [ -z "$(task_body "$content" | tr -d '[:space:]')" ]; then
  refuse "TASK BODY IS EMPTY: $path has no body under the frontmatter. A task's body states the human's ask, in the user's own terms — write it, and ground the write in their words with sr-file … --cite:user '<exact words the user wrote>'."
fi
exit 0
