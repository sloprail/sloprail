#!/usr/bin/env bash
# Template for a FILE-GUARD's script check. Copy it next to your file-guard.yaml and
# change only fine(). Everything else (reading the changeset, refusing what could not
# be read, the refusal format) is already right.
#
# It checks each file `match` selected, one at a time. A rule about the change as a
# whole reads `.changeset.commits`, `.changeset.others` and `.changeset.citations` too.
# The payload is described in file-guard.md, "What a check receives".
#
# exit 0 permits. To refuse, print {"reason":"..."} and exit 1.
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

# fine PATH CONTENT — the rule itself. Return 0 if CONTENT is acceptable; otherwise
# print one sentence saying what is wrong and how to fix it, and return 1.
fine() {
  local path="$1" content="$2"
  if ! grep -q 'CHANGE-ME' <<<"$content"; then
    return 0
  fi
  echo "replace this with what $path must hold, and how to fix it"
  return 1
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
