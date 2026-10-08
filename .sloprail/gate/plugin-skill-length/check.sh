#!/usr/bin/env bash
# plugin-skill-length's gate check, from the skill's gate-check-template.sh: a pending write of a
# file of a skill in the sloprail plugin fits the file-guard's line-cap-lib.sh cap.
set -euo pipefail

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1"; }

kind="$(field '.event.kind // ""')"
path="$(field '.event.path // ""')"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

. "$(dirname "$0")/../../file-guard/plugin-skill-length/line-cap-lib.sh" ||
  refuse "line-cap-lib.sh could not be loaded, so the line cap could not be applied"

# fine CONTENT — the rule itself. Return 0 if CONTENT is acceptable; otherwise
# print one sentence saying what is wrong and how to fix it, and return 1.
fine() {
  local why
  why="$(line_cap_fine "$1")" && return 0
  echo "$path: $why"
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
