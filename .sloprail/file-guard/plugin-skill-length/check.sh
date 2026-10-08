#!/usr/bin/env bash
# plugin-skill-length's file-guard check, from the skill's check-template.sh: every committed file
# of a skill in the sloprail plugin fits line-cap-lib.sh's cap.
set -uo pipefail

payload="$(cat)"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

# Anything but a readable Changeset is a refusal, never a pass: an empty stdin or a
# jq failure must not read as "no files, nothing wrong".
[ "$(printf '%s' "$payload" | jq -r '.event.kind // ""')" = "Changeset" ] ||
  refuse "expected a Changeset event, so the changed files could not be checked"
printf '%s' "$payload" | jq -e '.changeset.files | type == "array"' >/dev/null 2>&1 ||
  refuse "the changeset's files could not be read, so they could not be checked"
count="$(printf '%s' "$payload" | jq -r '.changeset.files | length')" || count=""
case "$count" in '' | *[!0-9]*) refuse "the changeset's files could not be read, so they could not be checked" ;; esac

. "$(dirname "$0")/line-cap-lib.sh" || refuse "line-cap-lib.sh could not be loaded, so the line cap could not be applied"

# fine PATH CONTENT — the rule itself. Return 0 if CONTENT is acceptable; otherwise
# print one sentence saying what is wrong and how to fix it, and return 1.
fine() {
  line_cap_fine "$2"
}

i=0
while [ "$i" -lt "$count" ]; do
  path="$(printf '%s' "$payload" | jq -r --argjson i "$i" '.changeset.files[$i].path')" ||
    refuse "could not read file $i of the changeset, so it could not be checked"
  status="$(printf '%s' "$payload" | jq -r --argjson i "$i" '.changeset.files[$i].status')" ||
    refuse "could not read $path from the changeset, so it could not be checked"
  i=$((i + 1))

  # A deleted file reaches a guard whose `deletions:` is include/only, with
  # oldContent and no newContent. Nothing remains to judge here; read
  # `.changeset.files[].oldContent` if losing the file is the rule's business.
  [ "$status" = "D" ] && continue

  content="$(printf '%s' "$payload" | jq -r --argjson i "$((i - 1))" '.changeset.files[$i].newContent')" ||
    refuse "could not read $path from the changeset, so it could not be checked"

  if ! why="$(fine "$path" "$content")"; then
    refuse "$path: $why"
  fi
done
exit 0
