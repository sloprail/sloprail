#!/usr/bin/env bash
# Template for a file-guard's script check. Copy it next to your file-guard.yaml
# and change only fine(). Everything else — reading the event, choosing which
# bytes to judge for each event kind, the resultKnown discipline, the refusal
# format — is already right; the plugin's authoring-slop rule refuses scripts
# that get those wrong.
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

# The bytes to judge, per event kind.
case "$kind" in
  PreFileCreate | PreFileUpdate)
    # Before the write lands. newContent is only reliable when resultKnown is
    # true (a shell edit's result can't be known in advance). Unknown: permit
    # here and let the settled file be judged after the write.
    if [ "$(field '.event.resultKnown')" != "true" ]; then
      exit 0
    fi
    content="$(field '.event.newContent // ""')"
    ;;
  PostFileCreate | PostFileUpdate)
    # After the write: judge the file as it now is on disk.
    [ -n "$path" ] || refuse "the event named no path, so this rule could not check it"
    abs="${SR_WORKSPACE:-.}/$path"
    [ -f "$abs" ] || exit 0
    content="$(cat "$abs")"
    ;;
  PreFileDelete | PostFileDelete)
    # Deleting a file is not this rule's business.
    exit 0
    ;;
  *)
    refuse "unexpected event kind '$kind' for $path; this rule only judges file writes"
    ;;
esac

if ! why="$(fine "$content")"; then
  refuse "$why"
fi
exit 0
