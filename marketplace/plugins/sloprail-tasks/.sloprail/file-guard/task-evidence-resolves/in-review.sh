#!/usr/bin/env bash
# `when` for the tool_result citation that proves a task's work: is the task in
# review? Exit 0 — it is, so the write must cite tool output. Exit 1 — it is not,
# so no citation is required.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 does not permit anything — it
# APPLIES the requirement. So every path this script cannot decide exits 0, the
# fail-closed direction; only exit 1 waives the citation, and only on a decided
# "not in review".
#
#   in-review.sh             the task's status after the write is in_review
#                            (task-review: an in_review task always carries proof)
#   in-review.sh --entering  the write MOVES the task into in_review — it was not
#                            there before (task-evidence-resolves: the transition
#                            is the claim). A create has no prior status, so
#                            creating a task directly in in_review enters it.
#
# The status is read with the product's own `sr-file validate --emit` against the
# plugin's schema, never a second opinion about where frontmatter ends. A task
# whose frontmatter does not validate has no status and is not in review — the
# checks after this refuse it for that, with a better message.
set -uo pipefail

# Undecidable without jq: apply the requirement (exit 0, fail-closed).
command -v jq >/dev/null 2>&1 || exit 0

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }
schema="${SR_GUARDRAIL_DIR:-.}/../../schemas/task.cue"
status_of() {
  printf '%s' "$1" | sr-file validate - --as .md --schema "$schema" --emit 2>/dev/null | jq -r '.status // empty' 2>/dev/null
}

kind="$(field '.event.kind // ""')"
case "$kind" in
  PreFileCreate | PreFileUpdate)
    # A result the engine could not compute is undecidable: apply (exit 0).
    [ "$(field '.event.resultKnown // false')" = "true" ] || exit 0
    content="$(field '.event.newContent // ""')"
    ;;
  PostFileCreate | PostFileUpdate)
    abs="${SR_WORKSPACE:-.}/$(field '.event.path // ""')"
    # Written and removed within the cycle: nothing landed, nothing claims review.
    [ -f "$abs" ] || exit 1
    # A settled file that cannot be read is undecidable: apply (exit 0).
    content="$(cat "$abs")" || exit 0
    ;;
  *)
    # A delete claims nothing.
    exit 1
    ;;
esac

[ "$(status_of "$content")" = "in_review" ] || exit 1

old_status=""
case "$kind" in
  *Update) old_status="$(status_of "$(field '.event.oldContent // ""')")" ;;
esac
if [ "${1:-}" = "--entering" ] && [ "$old_status" = "in_review" ]; then
  exit 1
fi

# It applies. The hint the refusal carries: what proof is, and the exact edit.
path="$(field '.event.path // ""')"
jq -n --arg path "$path" --arg from "${old_status:-in_progress}" '{hint: (
  "An in_review task claims the work is finished, so it must cite the tool output that proves it. " +
  "Run what proves the work (the tests, the build), then make the status change citing a line of that output:\n" +
  "  sr-file edit " + $path + " --old-string '\''status: " + $from + "'\'' --new-string '\''status: in_review'\'' --cite:tool_result '\''<exact line of the output>'\''\n" +
  "Your own summary, the user'\''s words, or an answer to a question are not tool output.")}'
exit 0
