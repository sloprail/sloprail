#!/usr/bin/env bash
# prepare for task-gate-is-grounded's judge: hand it the gate file's own text,
# its kind, and the sibling TASK.md's FULL content (frontmatter and body
# together) — so judge-gate.md.j2 never has to read the tree itself.
#
# NO CITATION EXTRACTION OR GROUNDING HAPPENS HERE. A gate carries no
# citations of its own — only TASK.md does, and that citation's grounding is
# task-body-is-human-authored's subject, already validated separately. This
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
# exit 0 with `{"skip": true}` ABSTAINS — no model call, no verdict, deferred
# to the Post/Stop after-check on the settled bytes; a non-zero exit fails the
# check closed.
set -uo pipefail

skip() { printf '{"skip": true}\n'; exit 0; }

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
root="${SR_WORKSPACE:-.}"

# WHERE THE GATE'S OWN BYTES COME FROM — same Pre/Post dispatch every guard in
# this plugin uses. An underivable Pre write (resultKnown != true) ABSTAINS
# rather than judging an empty/guessed gate_content — the settled bytes are
# judged once the Post event carries them instead.
kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    abs="$root/$path"
    [ -f "$abs" ] || skip
    gate_content="$(cat "$abs" 2>/dev/null || true)"
    ;;
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$event" | jq -r '.event.resultKnown // false' 2>/dev/null)"
    [ "$known" = "true" ] || skip
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
