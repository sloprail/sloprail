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
# This is the file-guard's entry: it reads the Changeset, each task's oldContent (at
# the range's base) against its newContent (at head). The gate's entry compares the
# pending bytes with the file on disk. The comparison itself is the library's.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset body_changed_lib_loaded
. "$lib_dir/body-changed-lib.sh" || exit 2
[ "${body_changed_lib_loaded:-}" = 1 ] || exit 2
lib_setup

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }
[ "$(field '.event.kind // ""')" = "Changeset" ] || exit 0
n="$(field '.changeset.files | length')" || exit 0
case "$n" in '' | *[!0-9]*) exit 0 ;; esac

# Every file is asked. One that sets an ask applies the requirement (lib_check exits
# 0 with its hint); only when none does is the citation waived.
i=0
while [ "$i" -lt "$n" ]; do
  idx="$i"
  i=$((i + 1))
  f() { field ".changeset.files[$idx]$1"; }
  status="$(f '.status')" || exit 0
  path="$(f '.path')" || exit 0
  # A delete is not this guard's business (deletions default to skip).
  [ "$status" = "D" ] && continue
  # A created task sets its ask.
  [ "$status" = "A" ] && applies
  # A field the status should carry and lacks is undecidable, not empty: apply.
  content="$(f '.newContent | if type == "string" then . else error("missing newContent") end')" || exit 0
  old_content=""
  if [ "$status" != "A" ]; then
    old_content="$(f '.oldContent | if type == "string" then . else error("missing oldContent") end')" || exit 0
  fi
  lib_check
done
exit 1
