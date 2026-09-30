#!/usr/bin/env bash
# prepare for task-gate-is-grounded's judge: hand it the gate file's own text,
# its kind, and the sibling TASK.md's FULL content (frontmatter and body
# together) — so judge-gate.md.j2 never has to read the tree itself.
#
# NO CITATION EXTRACTION OR GROUNDING HAPPENS HERE. A gate carries no
# citations of its own — only the write that sets a TASK.md's body does, and
# that grounding is task-body-is-human-authored's subject, already validated
# separately. This
# guard's only question is whether the gate is DERIVED FROM the task: for
# that, the judge is handed the task's content as it stands (whatever it
# currently says) and decides traceability directly against it, the same way
# a reviewer reads the ticket before reading the PR — no second citation
# pipeline duplicating what already validated the ticket itself.
#
# Output nests under `additionalContext`. Emits .gate_path, .gate_kind
# (script|prompt), .gate_content (the gate file's own bytes), and
# .task_content (the whole TASK.md, frontmatter and body).
#
# THE PREPARE CONTRACT: exit 0 with additionalContext proceeds to the judge;
# exit 0 with `{"skip": true}` ABSTAINS — no model call, no verdict; a non-zero
# exit fails the check closed.
set -uo pipefail

skip() { printf '{"skip": true}\n'; exit 0; }

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
root="${SR_WORKSPACE:-.}"

# WHERE THE GATE'S OWN BYTES COME FROM. This is the PreFileWrite gate's copy: the
# pending bytes, off the event. The gate's first check (require-known-result.sh)
# already refused an unknown result; this fails closed too rather than judging an
# empty gate_content.
kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
[ -n "$kind" ] || { echo "task-gate-is-grounded: could not read the event's kind, so it could not be checked" >&2; exit 2; }
case "$kind" in
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$event" | jq -r '.event.resultKnown // false' 2>/dev/null)"
    if [ "$known" != "true" ]; then
      echo "task-gate-is-grounded: the result of this write to $path could not be computed ahead of the write, so its gate could not be judged" >&2
      exit 1
    fi
    gate_content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    ;;
  *)
    skip
    ;;
esac

case "$path" in
  *.sh) gate_kind="script" ;;
  *.md) gate_kind="prompt" ;;
  *)    gate_kind="unknown" ;;
esac

# The sibling TASK.md is two levels up from gates/<file>.
gates_dir="$(dirname "$path")"
task_dir="$(dirname "$gates_dir")"
task_md="$root/$task_dir/TASK.md"
task_content="$(cat "$task_md" 2>/dev/null || true)"

jq -n \
  --arg path "$path" \
  --arg kind "$gate_kind" \
  --arg gate "$gate_content" \
  --arg task "$task_content" \
  '{additionalContext: {gate_path: $path, gate_kind: $kind, gate_content: $gate, task_content: $task}}'
