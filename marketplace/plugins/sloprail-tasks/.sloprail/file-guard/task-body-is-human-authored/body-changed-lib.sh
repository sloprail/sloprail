#!/usr/bin/env bash
# Shared by the task-body-is-human-authored gate and its file-guard: one library, two thin entries.
# The gate entry reads the pending write (lib_init, then a Pre kind); the file-guard
# entry reads the Changeset and calls lib_check once per file. lib_check reads no
# event: the entry sets `path`, `content` (the body's new bytes) and `old_content`.
#
# lib_check returns 1 when the change leaves the body byte-identical (the citation is
# waived), and exits 0 with a hint when it sets the ask.

lib_setup() {
  set -uo pipefail

  command -v jq >/dev/null 2>&1 || exit 0

  lib="$lib_dir/lib-body.sh"
  [ -f "$lib" ] || exit 0
  # A helper stopped by a syntax error runs only up to it (whether the `.` then
  # fails depends on the bash version); only its last-line sentinel proves it
  # loaded whole. Not loaded whole is undecidable: apply (exit 0), never waive.
  unset lib_body_loaded
  # shellcheck source=lib-body.sh
  . "$lib"
  [ "${lib_body_loaded:-}" = 1 ] || exit 0

  # applies: the change sets the ask. The hint the refusal carries says what to
  # cite and that frontmatter changes need nothing.
  applies() {
    jq -n --arg path "$path" '{hint: (
      "A task'\''s body is the user'\''s ask: cite the words of their message it restates (--cite:user is repeatable, one per message; in a commit, one Sloprail-Cites-User: trailer per message), and write the body in their terms:\n" +
      "  sr-file write " + $path + " --cite:user '\''<exact words the user wrote>'\'' <<'\''TASK'\'' ... TASK\n" +
      "A change to the frontmatter alone (status, priority, depends_on) needs no citation.")}'
    exit 0
  }
}

# lib_init is the gate's: the pending write's own bytes.
lib_init() {
  lib_setup

  payload="$(cat)"
  field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

  kind="$(field '.event.kind // ""')"
  path="$(field '.event.path // ""')"
  old_content="$(field '.event.oldContent // ""')"
}

lib_check() {
  [ "$(task_body "$content")" = "$(task_body "$old_content")" ] && return 1
  applies
}

body_changed_lib_loaded=1
