#!/usr/bin/env bash
# prepare for stage 2 of task-gates-hold: hand the judge every `.md` gate's
# prompt text under this task's gates/, so judge-gates.md.j2 never has to walk
# the directory itself.
#
# THE GATE, mirroring stage 1's own (a SEPARATE check — a passing stage 1 does
# not end the chain, so without this gate every task write would pay for a
# judge call it is not up for): only a transition INTO to_do/in_progress FROM
# backlog/blocked, and only when at least one gates/*.md file exists. Does NOT
# re-run the .sh gates — stage 1 already refused the write if any failed.
set -uo pipefail

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"
schema="$gdir/../../schemas/task.cue"

skip() { printf '{"skip": true}\n'; exit 0; }

[ -f "$schema" ] || skip

kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    # The engine declares newContentKnown on PostFileCreate and PostFileUpdate
    # (internal/filemod/module.go FieldNewContentKnown; authoring-guardrails/
    # events.md): false when it could not read the settled file — a link to a
    # FIFO or a device, or past the read cap. The gates are unseen: the prepare
    # fails, so the check fails closed (never a skip).
    if [ "$(printf '%s' "$event" | jq -r '.event.newContentKnown // false' 2>/dev/null)" != "true" ]; then
      echo "task-gates-hold: $path could not be read (not a regular file, or too large), so its gates could not be judged" >&2
      exit 1
    fi
    abs="$root/$path"
    [ -f "$abs" ] || skip
    new_content="$(cat "$abs" 2>/dev/null || true)"
    ;;
  *)
    skip
    ;;
esac

new_doc="$(printf '%s' "$new_content" | sr-file validate - --as .md --schema "$schema" --emit 2>/dev/null)"
new_status="$(printf '%s' "$new_doc" | jq -r '.status // empty' 2>/dev/null)"
case "$new_status" in
  to_do|in_progress) : ;;
  *) skip ;;
esac

old_content=""
case "$kind" in
  PostFileUpdate)
    old_content="$(printf '%s' "$event" | jq -r '.event.oldContent // ""' 2>/dev/null)"
    ;;
esac
old_status=""
if [ -n "$old_content" ]; then
  old_doc="$(printf '%s' "$old_content" | sr-file validate - --as .md --schema "$schema" --emit 2>/dev/null)"
  old_status="$(printf '%s' "$old_doc" | jq -r '.status // empty' 2>/dev/null)"
fi
case "$old_status" in
  backlog|blocked|"") : ;;
  to_do|in_progress|in_review) skip ;;
esac

task_dir="$(dirname "$path")"
gates_dir="$root/$task_dir/gates"
[ -d "$gates_dir" ] || skip

gates=""
found=0
while IFS= read -r -d '' g; do
  found=1
  gname="$(basename "$g")"
  body="$(cat "$g" 2>/dev/null || true)"
  gates="${gates}### gates/${gname}
${body}

"
done < <(find "$gates_dir" -maxdepth 1 -name '*.md' -type f -print0 2>/dev/null | sort -z)

[ "$found" -eq 1 ] || skip

jq -n --arg body "$new_content" --arg gates "$gates" --arg path "$path" \
  '{additionalContext: {task_body: $body, gates: $gates, path: $path}}'
