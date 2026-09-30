#!/usr/bin/env bash
# `when` for task-body-is-human-authored's citation requirement: does this write
# set the task's ask? Exit 0 — it does (a create, or a body change), so the write
# must cite the user's words. Exit 1 — it does not (a status, priority or
# depends_on edit leaves the body byte-identical), so no citation is required.
#
# Any other outcome makes the engine apply the requirement, so every path this
# script cannot decide exits 0 rather than waive it: no jq, the sibling library
# missing, an unreadable settled file, a Pre result the engine could not compute.
#
# This is the file-guard's copy: a Post kind compares `.event.newContent` (the settled
# file) with `.event.oldContent` (the session baseline). Both are read off the event,
# never the disk: at Stop the engine also asks about each PART of a change no
# citation rode on, with the event narrowed to that part, and the file on disk is
# only its last state.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset body_changed_lib_loaded
. "$lib_dir/body-changed-lib.sh" || exit 2
[ "${body_changed_lib_loaded:-}" = 1 ] || exit 2
lib_init
case "$kind" in
  PostFileCreate)
    applies
    ;;
  PostFileUpdate)
    # The engine declares newContentKnown on PostFileCreate and PostFileUpdate
    # (internal/filemod/module.go FieldNewContentKnown; authoring-guardrails/
    # events.md): false when it could not read the settled file — a link to a
    # FIFO or a device, or past the read cap. Undecidable: apply (exit 0).
    [ "$(field '.event.newContentKnown // false')" = "true" ] || exit 0
    content="$(field '.event.newContent // ""')"
    ;;
  *)
    # A delete is not this guard's business (deletions default to skip).
    exit 1
    ;;
esac
lib_check
