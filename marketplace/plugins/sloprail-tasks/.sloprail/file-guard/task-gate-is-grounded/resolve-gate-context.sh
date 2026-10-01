#!/usr/bin/env bash
# prepare for task-gate-is-grounded's judge: for every gate file the changeset
# touches, hand the judge the gate's own text, its kind, and the sibling TASK.md's
# FULL content (frontmatter and body together), so judge-gate.md.j2 never has to
# read the tree itself.
#
# NO CITATION EXTRACTION OR GROUNDING HAPPENS HERE. A gate carries no citations of
# its own — only the commit that sets a TASK.md's body does, and that grounding is
# task-body-is-human-authored's subject, already validated separately. This
# guard's only question is whether the gate is DERIVED FROM the task: for that, the
# judge is handed the task's content as it stands at head (whatever it currently
# says) and decides traceability directly against it, the same way a reviewer
# reads the ticket before reading the PR.
#
# The gates come from the changeset (committed bytes), the tasks from SR_TREE (the
# committed head), so an uncommitted task edit is never what a gate is judged
# against.
#
# Output nests under `additionalContext`: .gates, one {path, kind (script|prompt),
# content (the gate file's bytes), task_content (the whole TASK.md)} per gate.
#
# THE PREPARE CONTRACT: exit 0 with additionalContext proceeds to the judge; exit 0
# with `{"skip": true}` ABSTAINS — no model call, no verdict; a non-zero exit fails
# the check closed.
set -uo pipefail

skip() { printf '{"skip": true}\n'; exit 0; }

fail() {
  echo "task-gate-is-grounded: $1" >&2
  exit 1
}

event="$(cat)"
[ "$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)" = "Changeset" ] ||
  fail "expected a Changeset event, so the gates could not be judged"
[ -n "${SR_TREE:-}" ] || fail "SR_TREE is not set, so the committed tasks could not be read"

# The changeset's files are read through this plugin's one library (a missing
# content field is undecidable, never an empty file).
cs_lib="$(dirname "$0")/../../lib/changeset.sh"
unset changeset_lib_loaded
. "$cs_lib" 2>/dev/null || fail "the changeset library (lib/changeset.sh) could not be loaded"
[ "${changeset_lib_loaded:-}" = 1 ] || fail "the changeset library (lib/changeset.sh) could not be loaded"
n="$(cs_count "$event")" || fail "the changeset's files could not be read"
case "$n" in '' | *[!0-9]*) fail "the changeset's files could not be read" ;; esac

gates='[]'
i=0
while [ "$i" -lt "$n" ]; do
  path="$(cs_get "$event" "$i" .path)" || fail "could not read file $i of the changeset"
  gate_content="$(cs_text "$event" "$i" newContent)" || fail "could not read $path from the changeset"
  i=$((i + 1))

  case "$path" in
    *.sh) gate_kind="script" ;;
    *.md) gate_kind="prompt" ;;
    *)    gate_kind="unknown" ;;
  esac

  # The sibling TASK.md is two levels up from gates/<file>. A task that is not
  # there (never committed, or removed) leaves the judge an empty task to judge
  # the gate against, which it fails as untraceable.
  task_dir="$(dirname "$(dirname "$path")")"
  task_content=""
  if [ -f "$SR_TREE/$task_dir/TASK.md" ]; then
    task_content="$(cat "$SR_TREE/$task_dir/TASK.md")" || fail "could not read $task_dir/TASK.md"
  fi

  gates="$(printf '%s' "$gates" | jq -c --arg path "$path" --arg kind "$gate_kind" --arg gate "$gate_content" --arg task "$task_content" \
    '. + [{path: $path, kind: $kind, content: $gate, task_content: $task}]')" || fail "could not assemble the input for $path"
done

[ "$gates" = "[]" ] && skip

jq -n --argjson gates "$gates" '{additionalContext: {gates: $gates}}'
