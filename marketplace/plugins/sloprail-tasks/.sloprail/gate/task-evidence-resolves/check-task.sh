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
# THE PRE-WRITE HALF. This is the gate's copy: it reads the pending bytes off the
# event. The plain file-guard of the same name carries the Stop copy, over the
# settled bytes on disk.
#
# THE REFUSAL CONTRACT (internal/dispatch/exec.go): exit 0 permits; any non-zero
# exit refuses, carrying `{"reason": "..."}` on stdout. Fails closed throughout —
# everything it needs (the schema, the filesystem, the event) is local, so a
# failure here is a defect to surface rather than a flaky call to permit past.
#
# `set -uo pipefail`, never `set -e`.
lib_dir="$(cd "$(dirname "$0")/../../file-guard/task-evidence-resolves" && pwd)"
unset check_task_lib_loaded
. "$lib_dir/check-task-lib.sh" || exit 2
[ "${check_task_lib_loaded:-}" = 1 ] || exit 2
lib_init
# WHERE THE BYTES COME FROM. This is the PreFileWrite gate's copy: the pending
# bytes, off the event. resultKnown false means the engine could not compute them
# (a sed -i, a notebook create, an unresolvable sr-file line): refuse — a write
# nobody saw is not approved.
kind="$(field '.event.kind // ""')"
case "$kind" in
  PreFileCreate | PreFileUpdate)
    if [ "$(field '.event.resultKnown // false')" != "true" ]; then
      refuse "task-evidence-resolves: the result of this write to $path could not be computed ahead of the write (a sed -i, a notebook create, an unresolvable sr-file line), so it could not be checked. Write the file content directly (the Write tool), or run sr-file on its own line."
    fi
    content="$(field '.event.newContent // ""')"
    ;;
  *)
    # A delete, or a kind this gate is not about: nothing to check.
    exit 0
    ;;
esac
lib_check
