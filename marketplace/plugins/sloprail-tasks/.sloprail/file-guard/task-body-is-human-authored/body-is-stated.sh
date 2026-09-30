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
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset body_is_stated_lib_loaded
. "$lib_dir/body-is-stated-lib.sh" || exit 2
[ "${body_is_stated_lib_loaded:-}" = 1 ] || exit 2
lib_init
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
lib_check
