#!/usr/bin/env bash
# Template for a FILE-GUARD's script check. Copy it next to your file-guard.yaml
# and change only fine(). Everything else — reading the event, the newContentKnown
# discipline, the refusal format — is already right; the plugin's authoring-slop
# rule refuses scripts that get those wrong.
#
# A file-guard judges the settled file at Stop, so it only ever receives Post*
# kinds. To refuse a write BEFORE it lands, write a gate instead: see
# gate-check-template.sh.
#
# Contract: stdin is the CheckPayload ({"event":{...},"transcriptPath":...}).
# exit 0 permits. To refuse, print {"reason":"..."} and exit 1.
set -euo pipefail

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1"; }

kind="$(field '.event.kind // ""')"
path="$(field '.event.path // ""')"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

# fine CONTENT — the rule itself. Return 0 if CONTENT is acceptable; otherwise
# print one sentence saying what is wrong and how to fix it, and return 1.
fine() {
  local content="$1"
  if ! printf '%s' "$content" | grep -q 'CHANGE-ME'; then
    return 0
  fi
  echo "replace this with what $path must hold, and how to fix it"
  return 1
}

# The bytes to judge.
case "$kind" in
  PostFileCreate | PostFileUpdate)
    # The settled bytes, which the engine read for you — unless it could not
    # (newContentKnown false: a link to a FIFO or a device, or a file past the read
    # cap). A file this rule cannot see is refused, not waved through as empty.
    [ -n "$path" ] || refuse "the event named no path, so this rule could not check it"
    [ "$(field '.event.newContentKnown')" = "true" ] ||
      refuse "$path could not be read (not a regular file, or too large), so this rule could not check it"
    content="$(field '.event.newContent // ""')"
    ;;
  PostFileDelete)
    # Only reaches a guard whose `deletions:` is include/only. Nothing remains to
    # judge here; read .event.oldContent if losing the file is the rule's business.
    exit 0
    ;;
  *)
    refuse "unexpected event kind '$kind' for $path; a file-guard only judges settled files"
    ;;
esac

if ! why="$(fine "$content")"; then
  refuse "$why"
fi
exit 0
