#!/usr/bin/env bash
# Refuse a pending write of a SKILL.md that is too large to judge. Reads the pending
# write's bytes; a write whose result the engine could not compute is refused first,
# since bytes nobody saw have not been measured. The cap is size-cap-lib.sh's.
set -uo pipefail

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1"; }

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

kind="$(field '.event.kind // ""')"
path="$(field '.event.path // ""')"
case "$kind" in
  PreFileCreate | PreFileUpdate) ;;
  *) refuse "unexpected event kind '$kind' for $path; this gate only judges pending file writes" ;;
esac
[ -n "$path" ] || refuse "the event named no path, so this rule could not check it"

[ "$(field '.event.resultKnown // false')" = "true" ] ||
  refuse "the result of this write to $path could not be computed ahead of time (an in-place or environment-dependent edit, or a notebook create), so its size could not be checked before it lands. Write the file content directly."

lib_dir="$(cd "$(dirname "$0")/../../file-guard/skill-quality" && pwd)"
. "$lib_dir/size-cap-lib.sh" || refuse "skill-quality: size-cap-lib.sh could not be loaded, so the size cap could not be applied"

bytes="$(printf '%s' "$payload" | jq -j '.event.newContent // ""' | wc -c | tr -d ' ')" ||
  refuse "the pending content of $path could not be read, so its size could not be checked"
if why="$(size_cap_refuses "$bytes" "$path" 2>&1)"; then
  refuse "$why"
fi
exit 0
