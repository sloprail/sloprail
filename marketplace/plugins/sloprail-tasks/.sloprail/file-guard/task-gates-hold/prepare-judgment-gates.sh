#!/usr/bin/env bash
# prepare for stage 2 of task-gates-hold: hand the judge every `.md` gate's
# prompt text under the gates/ of each task in the changeset that is making the
# transition, so judge-gates.md.j2 never has to walk the directories itself.
#
# THE GATE, mirroring stage 1's own (a SEPARATE check — a passing stage 1 does
# not end the chain, so without this gate every changeset would pay for a
# judge call it is not up for): only a task whose status went INTO to_do/
# in_progress FROM backlog/blocked (or from nothing, for a task the range added)
# is judged, and only when it has at least one gates/*.md file. Reads the
# changeset's committed bytes and SR_TREE. Does NOT
# re-run the .sh gates — stage 1 already refused the write if any failed.
set -uo pipefail

event="$(cat)"

root="${SR_TREE:-}"
gdir="${SR_GUARDRAIL_DIR:-.}"
schema="$gdir/../../schemas/task.cue"

fail() { echo "$1" >&2; exit 1; }
skip() { printf '{"skip": true}\n'; exit 0; }

[ "$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)" = "Changeset" ] ||
  fail "task-gates-hold: expected a Changeset event, so the tasks could not be judged"
[ -n "$root" ] || fail "task-gates-hold: SR_TREE is not set, so the committed tasks could not be read"
[ -f "$schema" ] || skip

n="$(printf '%s' "$event" | jq -r '.changeset.files | length')" || fail "task-gates-hold: the changeset's files could not be read"
case "$n" in '' | *[!0-9]*) fail "task-gates-hold: the changeset's files could not be read" ;; esac

tasks='[]'
i=0
while [ "$i" -lt "$n" ]; do
  path="$(printf '%s' "$event" | jq -r --argjson i "$i" '.changeset.files[$i].path')" || fail "task-gates-hold: could not read file $i of the changeset"
  status="$(printf '%s' "$event" | jq -r --argjson i "$i" '.changeset.files[$i].status')" || fail "task-gates-hold: could not read $path from the changeset"
  new_content="$(printf '%s' "$event" | jq -r --argjson i "$i" '.changeset.files[$i].newContent | if type == "string" then . else error("missing newContent") end')" || fail "task-gates-hold: could not read $path from the changeset"
  old_content="$(printf '%s' "$event" | jq -r --argjson i "$i" '.changeset.files[$i].oldContent | if type == "string" then . else error("missing oldContent") end')" || fail "task-gates-hold: could not read the earlier $path from the changeset"
  i=$((i + 1))
  [ -n "$new_content" ] || continue

  new_doc="$(printf '%s' "$new_content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)" || fail "task-gates-hold: sr-file could not validate $path, so its status is unknown: $new_doc"
  new_status="$(printf '%s' "$new_doc" | jq -r '.status // empty' 2>/dev/null)"
  [ -n "$new_status" ] || fail "task-gates-hold: $path carries no readable status, so its gates could not be checked"
  case "$new_status" in
    to_do|in_progress) : ;;
    *) continue ;;
  esac

  old_status=""
  if [ "$status" != "A" ] && [ -n "$old_content" ]; then
    old_doc="$(printf '%s' "$old_content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)" || fail "task-gates-hold: sr-file could not validate the previous $path: $old_doc"
    old_status="$(printf '%s' "$old_doc" | jq -r '.status // empty' 2>/dev/null)"
  fi
  case "$old_status" in
    backlog|blocked|"") : ;;
    to_do|in_progress|in_review) continue ;;
  esac

  task_dir="$(dirname "$path")"
  gates_dir="$root/$task_dir/gates"
  [ -d "$gates_dir" ] || continue

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
  [ "$found" -eq 1 ] || continue

  tasks="$(printf '%s' "$tasks" | jq -c --arg path "$path" --arg body "$new_content" --arg gates "$gates" \
    '. + [{path: $path, task_body: $body, gates: $gates}]')" || fail "task-gates-hold: could not assemble the input for $path"
done

[ "$tasks" = "[]" ] && skip

jq -n --argjson tasks "$tasks" '{additionalContext: {tasks: $tasks}}'
