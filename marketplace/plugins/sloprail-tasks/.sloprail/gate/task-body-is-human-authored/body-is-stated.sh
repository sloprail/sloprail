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
lib_dir="$(cd "$(dirname "$0")/../../file-guard/task-body-is-human-authored" && pwd)"
unset body_is_stated_lib_loaded
. "$lib_dir/body-is-stated-lib.sh" || exit 2
[ "${body_is_stated_lib_loaded:-}" = 1 ] || exit 2
lib_init
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
lib_check
