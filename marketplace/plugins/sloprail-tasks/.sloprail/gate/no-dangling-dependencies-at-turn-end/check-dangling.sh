#!/usr/bin/env bash
# A turn may not end while ANY task's depends_on names a task folder that does
# not exist — the CHECKPOINT that keeps deleting a task and stripping it from
# every dependent's depends_on ONE ATOMIC CHANGE, never two.
#
# WHY THIS EXISTS. task-dependencies-resolve refuses a dependent task from
# ENTERING to_do/in_progress/in_review while a depends_on id resolves to
# nothing (see its own header for why it does not distinguish "unfinished"
# from "never existed" — both read as "not there"). But a task can sit in
# to_do citing a dependency that gets deleted LATER, mid-turn or in a later
# turn, and nothing until now re-checked depends_on once a task had already
# passed that gate. Without this rule, deleting memories/tasks/auth/migrate
# while memories/tasks/web/launch/TASK.md still lists "auth/migrate" in its
# depends_on leaves a REFERENCE TO NOTHING sitting in the tree — indistin-
# guishable, to a human OR to task-dependencies-resolve reading it fresh next
# time, from "auth/migrate was never a real task, this is a typo". The
# reviewer who deletes a task is the one who KNOWS it is done and knows who
# depended on it; requiring the strip in the SAME turn (or earlier) rather
# than leaving it for whoever next reads the dependent is what keeps that
# knowledge from being lost.
#
# A GATE, on Stop, with NO match (Stop carries no fields) — the same shape
# no-unfinished-work-at-turn-end uses. It reads $SR_WORKSPACE's task tree
# itself rather than being handed a subject.
#
# THE CHECK: for every memories/tasks/<g>/<n>/TASK.md, for every id in its
# depends_on, does memories/tasks/<id>/TASK.md exist? If not, that dependent's
# depends_on is DANGLING and the turn is refused, naming the dependent and the
# missing id — the fix is either to strip the id (the dependency really is
# done) or to restore the dependency (the deletion was a mistake).
#
# TWO FAILURE DIRECTIONS, the same split no-unfinished-work-at-turn-end uses:
#   LOGIC    -> FAIL CLOSED. A dangling reference is a refusal. There is no
#               path where one is permitted to stand at turn end.
#   PLUMBING -> FAIL OPEN, exit 0, and say on stderr which path was taken. No
#               SR_WORKSPACE, no tasks directory, no sr-file, no jq — none of
#               those is evidence of a dangling reference, and refusing on
#               them wedges a session the agent cannot un-wedge.
#
# THE REFUSAL CONTRACT: exit 0 permits; non-zero refuses with
# `{"reason": "..."}` on stdout.
set -uo pipefail

payload="$(cat)"
: "${payload:=}"

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}
say() { printf '%s\n' "$*" >&2; }

# ---------------------------------------------------------------- plumbing ---
root="${SR_WORKSPACE:-}"
if [ -z "$root" ]; then
  say "no-dangling-dependencies-at-turn-end: PLUMBING FAIL-OPEN — SR_WORKSPACE is unset, so there is no tree to look in. Permitting."
  exit 0
fi

tasks_dir="$root/memories/tasks"
if [ ! -d "$tasks_dir" ]; then
  say "no-dangling-dependencies-at-turn-end: PLUMBING FAIL-OPEN — no tasks directory at $tasks_dir. Permitting."
  exit 0
fi

schema="$root/.sloprail/schemas/task.cue"
if [ ! -f "$schema" ]; then
  say "no-dangling-dependencies-at-turn-end: PLUMBING FAIL-OPEN — schema missing at $schema, so depends_on cannot be read. Permitting."
  exit 0
fi

command -v sr-file >/dev/null 2>&1 || {
  say "no-dangling-dependencies-at-turn-end: PLUMBING FAIL-OPEN — sr-file not on PATH. Permitting."
  exit 0
}
command -v jq >/dev/null 2>&1 || {
  say "no-dangling-dependencies-at-turn-end: PLUMBING FAIL-OPEN — jq not on PATH. Permitting."
  exit 0
}

# ------------------------------------------------------------------- logic ---
dangling=""
dangling_count=0
invalid_count=0

while IFS= read -r -d '' f; do
  rel="${f#"$root"/}"
  id="$(printf '%s' "$rel" | sed -E 's#^memories/tasks/([^/]+)/([^/]+)/TASK\.md$#\1/\2#')"

  doc="$(sr-file validate "$f" --schema "$schema" --emit 2>/dev/null)"
  rc=$?
  if [ $rc -ne 0 ] || [ -z "$doc" ]; then
    # SCHEMA-INVALID: skipped on purpose, same reasoning as
    # no-unfinished-work-at-turn-end — task-evidence-resolves owns that
    # refusal at write time, with a better message.
    invalid_count=$((invalid_count + 1))
    continue
  fi

  deps="$(printf '%s' "$doc" | jq -r '(.depends_on // [])[]' 2>/dev/null)"
  [ -n "$deps" ] || continue

  while IFS= read -r dep; do
    [ -n "$dep" ] || continue
    if [ ! -d "$root/memories/tasks/$dep" ]; then
      dangling_count=$((dangling_count + 1))
      dangling="${dangling}  ${rel} (${id}) depends_on \"${dep}\" — memories/tasks/${dep} does not exist
"
    fi
  done <<EOF
$deps
EOF
done < <(find "$tasks_dir" -mindepth 3 -maxdepth 3 -name 'TASK.md' -type f -print0 2>/dev/null | sort -z)

if [ "$invalid_count" -gt 0 ]; then
  say "no-dangling-dependencies-at-turn-end: skipped $invalid_count task file(s) whose frontmatter does not satisfy the schema — task-evidence-resolves owns those."
fi

if [ "$dangling_count" -eq 0 ]; then
  exit 0
fi

if [ "$dangling_count" -eq 1 ]; then
  headline="A turn cannot end with a dangling dependency: 1 depends_on entry names a task that no longer exists."
else
  headline="A turn cannot end with dangling dependencies: $dangling_count depends_on entries name tasks that no longer exist."
fi

tail="Deleting a task and leaving another task's depends_on pointing at it is not
one change, it is a change left half-done. For each entry above: if the
dependency really is finished, remove its id from the dependent's depends_on
in THIS turn (the same change that deleted it, or before ending this one); if
the deletion was a mistake, restore the dependency instead. Do not leave a
depends_on id pointing at nothing."

refuse "$headline

$dangling
$tail"
