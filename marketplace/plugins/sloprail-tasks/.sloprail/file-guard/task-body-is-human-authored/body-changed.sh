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
# THE TWO MOMENTS match the checks': a Pre kind compares the pending bytes with
# `.event.oldContent` (the file on disk); a Post kind reads the settled file and
# compares it with `.event.oldContent` (the session baseline).
set -uo pipefail

command -v jq >/dev/null 2>&1 || exit 0

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

lib="${SR_GUARDRAIL_DIR:-.}/lib-body.sh"
[ -f "$lib" ] || exit 0
# shellcheck source=lib-body.sh
. "$lib"

kind="$(field '.event.kind // ""')"
case "$kind" in
  PreFileCreate | PostFileCreate)
    exit 0
    ;;
  PreFileUpdate)
    [ "$(field '.event.resultKnown // false')" = "true" ] || exit 0
    content="$(field '.event.newContent // ""')"
    ;;
  PostFileUpdate)
    abs="${SR_WORKSPACE:-.}/$(field '.event.path // ""')"
    content="$(cat "$abs" 2>/dev/null)" || exit 0
    ;;
  *)
    # A delete is not this guard's business (deletions default to skip).
    exit 1
    ;;
esac

[ "$(task_body "$content")" = "$(task_body "$(field '.event.oldContent // ""')")" ] && exit 1
exit 0
