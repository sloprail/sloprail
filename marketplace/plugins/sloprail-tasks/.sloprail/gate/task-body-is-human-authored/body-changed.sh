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
# This is the gate's copy: a Pre kind compares the pending bytes (`.event.newContent`)
# with `.event.oldContent` (the file on disk); the file-guard's Stop copy compares the
# settled bytes with the session baseline.
lib_dir="$(cd "$(dirname "$0")/../../file-guard/task-body-is-human-authored" && pwd)"
unset body_changed_lib_loaded
. "$lib_dir/body-changed-lib.sh" || exit 2
[ "${body_changed_lib_loaded:-}" = 1 ] || exit 2
lib_init
case "$kind" in
  PreFileCreate)
    applies
    ;;
  PreFileUpdate)
    [ "$(field '.event.resultKnown // false')" = "true" ] || exit 0
    content="$(field '.event.newContent // ""')"
    ;;
  *)
    # A delete is not this gate's business.
    exit 1
    ;;
esac
lib_check
