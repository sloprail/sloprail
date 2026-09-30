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
#      demands a tool_result citation on a write that moves the task INTO
#      in_review (`when: ./in-review.sh --entering`), and the engine refuses an
#      uncited one before this runs. The task file carries no transcript path of
#      any kind.
#
# WHAT THIS IS NOT. It does NOT check the BODY's grounding in the user's ask — that
# is task-body-is-human-authored's, against the `user` pool. And it makes NO
# judgement about whether the cited output SUBSTANTIATES the claim — that is
# task-review's model call. See the plugin README for the three-way split.
#
# THE STOP HALF. This is the file-guard's copy: it reads the settled bytes on disk.
# The PreFileWrite gate of the same name carries the pending-bytes copy.
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
lib_init
# WHERE THE BYTES COME FROM. This is the file-guard's copy: the settled file at
# Stop, off the disk.
kind="$(field '.event.kind // ""')"
case "$kind" in
  PostFileCreate | PostFileUpdate)
    # newContentKnown (declared on the Post kinds, internal/filemod/module.go)
    # false: the engine could not read the settled file — a link to a FIFO or a
    # device, or past the read cap. Unseen: refuse rather than pass unchecked.
    [ "$(field '.event.newContentKnown // false')" = "true" ] ||
      refuse "task-evidence-resolves: $path could not be read (not a regular file, or too large), so it could not be checked"
    abs="$root/$path"
    # Written and then removed within the cycle: nothing to check, nothing wrong.
    [ -f "$abs" ] || exit 0
    content="$(cat "$abs")" || refuse "task-evidence-resolves: could not read $path to check it"
    ;;
  *)
    # A delete, or a kind this guard is not about: nothing to check.
    exit 0
    ;;
esac
lib_check
