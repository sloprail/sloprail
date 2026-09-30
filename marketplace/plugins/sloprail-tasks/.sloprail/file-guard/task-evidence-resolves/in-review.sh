#!/usr/bin/env bash
# `when` for the tool_result citation that proves a task's work: is the task in
# review? Exit 0 — one is, so the range's commits must cite tool output. Exit 1 — it is not,
# so no citation is required.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 does not permit anything — it
# APPLIES the requirement. So every path this script cannot decide exits 0, the
# fail-closed direction; only exit 1 waives the citation, and only on a decided
# "not in review".
#
#   in-review.sh             some task's status at head is in_review
#                            (task-review: an in_review task always carries proof)
#   in-review.sh --entering  the range MOVES some task into in_review — it was not
#                            there before (task-evidence-resolves: the transition
#                            is the claim). A create has no prior status, so
#                            creating a task directly in in_review enters it.
#
# This copy serves the file-guards (task-evidence-resolves, task-review) and reads
# a Changeset: each file's newContent is its status at head, its oldContent its
# status at the range's base. The PreFileWrite gate of the same name carries the
# pending copy.
#
# The status is read with the product's own `sr-file validate --emit` against the
# plugin's schema, never a second opinion about where frontmatter ends. A task
# whose frontmatter does not validate has no status and is not in review — the
# checks after this refuse it for that, with a better message.
set -uo pipefail

# Undecidable without jq: apply the requirement (exit 0, fail-closed).
# DELIBERATE, and fail-closed: in a `when` predicate exit 0 APPLIES the requirement (only exit 1 waives it), so a missing tool or helper applies it rather than permitting.
command -v jq >/dev/null 2>&1 || exit 0

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }
schema="${SR_GUARDRAIL_DIR:-.}/../../schemas/task.cue"
status_of() {
  printf '%s' "$1" | sr-file validate - --as .md --schema "$schema" --emit 2>/dev/null | jq -r '.status // empty' 2>/dev/null
}

[ "$(field '.event.kind // ""')" = "Changeset" ] || exit 0
# The changeset's files are read through this plugin's one library (a missing
# content field is undecidable, never an empty file).
cs_lib="$(dirname "$0")/../../lib/changeset.sh"
unset changeset_lib_loaded
. "$cs_lib" 2>/dev/null || exit 0
[ "${changeset_lib_loaded:-}" = 1 ] || exit 0
n="$(cs_count "$payload")" || exit 0
case "$n" in '' | *[!0-9]*) exit 0 ;; esac

i=0
while [ "$i" -lt "$n" ]; do
  idx="$i"
  i=$((i + 1))
  f() { field ".changeset.files[$idx]$1"; }
  status="$(f '.status')" || exit 0
  path="$(f '.path')" || exit 0

  # A delete claims nothing.
  [ "$status" = "D" ] && continue

  # A field the status should carry and lacks is undecidable, not empty: apply.
  content="$(cs_text "$payload" "$idx" newContent)" || exit 0
  [ "$(status_of "$content")" = "in_review" ] || continue

  old_status=""
  if [ "$status" != "A" ]; then
    old_content="$(cs_text "$payload" "$idx" oldContent)" || exit 0
    old_status="$(status_of "$old_content")"
  fi
  if [ "${1:-}" = "--entering" ] && [ "$old_status" = "in_review" ]; then
    continue
  fi

  # It applies. The hint the refusal carries: what proof is, and the exact command —
  # the commit that moves the task to in_review carries the output as a trailer.
  how="  git commit -m 'Move the task to in_review' -m 'Sloprail-Cites-Tool: <exact line of the output>'   ($path)"
  jq -n --arg how "$how" '{hint: (
    "An in_review task claims the work is finished, so its commit must cite the tool output that proves it. " +
    "Run what proves the work (the tests, the build), then commit the change citing a line of that output:\n" +
    $how + "\nYour own summary, the user'\''s words, or an answer to a question are not tool output.")}'
  exit 0
done
exit 1
