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
# A helper stopped by a syntax error runs only up to it (whether the `.` then
# fails depends on the bash version); only its last-line sentinel proves it
# loaded whole. Not loaded whole is undecidable: apply (exit 0), never waive.
unset lib_body_loaded
# shellcheck source=lib-body.sh
. "$lib"
[ "${lib_body_loaded:-}" = 1 ] || exit 0

# applies: the write sets the ask. The hint the refusal carries says what to
# cite and that frontmatter changes need nothing.
applies() {
  jq -n --arg path "$(field '.event.path // ""')" '{hint: (
    "A task'\''s body is the user'\''s ask: cite the words of their message it restates (--cite:user is repeatable, one per message), and write the body in their terms:\n" +
    "  sr-file write " + $path + " --cite:user '\''<exact words the user wrote>'\'' <<'\''TASK'\'' ... TASK\n" +
    "A change to the frontmatter alone (status, priority, depends_on) needs no citation.")}'
  exit 0
}

kind="$(field '.event.kind // ""')"
case "$kind" in
  PreFileCreate | PostFileCreate)
    applies
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
applies
