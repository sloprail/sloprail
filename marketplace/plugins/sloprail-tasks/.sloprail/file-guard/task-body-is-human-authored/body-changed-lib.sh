#!/usr/bin/env bash
# Shared by the task-body-is-human-authored gate and its file-guard: one library, two thin entries.
# Each entry reads the event kind and its own bytes; nothing that follows branches
# on it.

lib_init() {
set -uo pipefail

command -v jq >/dev/null 2>&1 || exit 0

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

lib="$lib_dir/lib-body.sh"
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
}

lib_check() {

[ "$(task_body "$content")" = "$(task_body "$(field '.event.oldContent // ""')")" ] && exit 1
applies
}

body_changed_lib_loaded=1
