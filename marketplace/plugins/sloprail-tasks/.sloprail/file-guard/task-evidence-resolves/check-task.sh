#!/usr/bin/env bash
# Refuses a TASK.md whose frontmatter does not satisfy the plugin's task.cue, or
# whose artifacts do not resolve or, in in_review, are missing.
#
# THE DETERMINISTIC FLOOR. Three questions, no model:
#   1. SCHEMA — the product's own `sr-file validate` vets the frontmatter against
#      CUE (status is one of five literals, priority one of four, artifacts are
#      repo-relative tree citations, no invented field). --emit hands this script
#      the validated frontmatter as JSON; it parses none of its own.
#   2. ARTIFACTS RESOLVE — every `<repo-relative-file>:<ranges>` names a file under
#      the repo whose cited lines exist; an in_review task names at least one.
#   3. PROOF RIDES ON THE TRANSITION — not this script's: the guard's `require`
#      demands a tool_result citation (a Sloprail-Cites-Tool: trailer on a commit
#      of the range) when a task moves INTO in_review (`when: ./in-review.sh --entering`), and the engine refuses an
#      uncited one before this runs. The task file carries no transcript path of
#      any kind.
#
# WHAT THIS IS NOT. It does NOT check the BODY's grounding in the user's ask — that
# is task-body-is-human-authored's, against the `user` pool. And it makes NO
# judgement about whether the cited output SUBSTANTIATES the claim — that is
# task-review's model call. See the plugin README for the three-way split.
#
# THE FILE-GUARD ENTRY. It checks every task in the changeset from its committed
# bytes, and resolves artifacts against SR_TREE (the committed head), not the
# working tree. The PreFileWrite gate of the same name carries the pending-bytes entry.
#
# THE REFUSAL CONTRACT (internal/dispatch/exec.go): exit 0 permits; any non-zero
# exit refuses, carrying `{"reason": "..."}` on stdout. Fails closed throughout —
# everything it needs (the schema, the filesystem, the event) is local, so a
# failure here is a defect to surface rather than a flaky call to permit past.
#
# `set -uo pipefail`, never `set -e`.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset check_task_lib_loaded
. "$lib_dir/check-task-lib.sh" || exit 2
[ "${check_task_lib_loaded:-}" = 1 ] || exit 2
lib_setup

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }
[ "$(field '.event.kind // ""')" = "Changeset" ] ||
  refuse "task-evidence-resolves: expected a Changeset event, so the tasks could not be checked"
root="${SR_TREE:-}"
[ -n "$root" ] || refuse "task-evidence-resolves: SR_TREE is not set, so the committed tree could not be read"
file_n="$(field '.changeset.files | length')" || true
case "$file_n" in '' | *[!0-9]*) refuse "task-evidence-resolves: the changeset's files could not be read, so nothing could be checked" ;; esac

file_i=0
while [ "$file_i" -lt "$file_n" ]; do
  path="$(printf '%s' "$payload" | jq -r --argjson i "$file_i" '.changeset.files[$i].path')" ||
    refuse "task-evidence-resolves: could not read file $file_i of the changeset"
  content="$(printf '%s' "$payload" | jq -r --argjson i "$file_i" '.changeset.files[$i].newContent | if type == "string" then . else error("missing newContent") end')" ||
    refuse "task-evidence-resolves: could not read $path from the changeset"
  file_i=$((file_i + 1))
  lib_check
done
exit 0
