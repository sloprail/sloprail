#!/usr/bin/env bash
# Template for a GATE's script check on a pre-write event (`on: PreFileWrite`).
# Copy it next to your gate.yaml and change only fine(). A gate PREVENTS: it sees
# the write before it lands, and the engine does not fail it closed for you — a
# write whose result could not be computed (a sed -i, a notebook create, an sr-file
# line it could not resolve) arrives with resultKnown false and an empty
# newContent, and this template refuses it rather than admit bytes nobody saw.
# Keep a plain file-guard of the same name (file-guard's check-template.sh) for
# the settled result.
#
# Contract: stdin is the GateCheckPayload ({"event":{...},"transcriptPath":...}).
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
  if ! grep -q 'CHANGE-ME' <<<"$content"; then
    return 0
  fi
  echo "replace this with what $path must hold, and how to fix it"
  return 1
}

# The bytes to judge.
case "$kind" in
  PreFileCreate | PreFileUpdate)
    [ -n "$path" ] || refuse "the event named no path, so this rule could not check it"
    [ "$(field '.event.resultKnown')" = "true" ] ||
      refuse "the result of this write to $path could not be computed ahead of time (an in-place or environment-dependent edit), so it cannot be checked before it lands. Write the file content directly."
    content="$(field '.event.newContent // ""')"
    ;;
  *)
    refuse "unexpected event kind '$kind' for $path; this gate only judges pending file writes"
    ;;
esac

if ! why="$(fine "$content")"; then
  refuse "$why"
fi
exit 0
