#!/usr/bin/env bash
# Refuse a write inside a unit folder whose UNIT.md does not exist yet.
# Path-based, not content-based, so an unresolvable pre-write result is still
# judged.
set -euo pipefail

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1"; }

kind="$(field '.event.kind // ""')"
path="$(field '.event.path // ""')"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

case "$kind" in
  PreFileCreate | PreFileUpdate | PostFileCreate | PostFileUpdate) ;;
  PreFileDelete | PostFileDelete) exit 0 ;;
  *) refuse "unexpected event kind '$kind' for $path; this rule only judges file writes" ;;
esac

[ -n "$path" ] || refuse "the event named no path, so this rule could not check it"
unit_dir="$(dirname "$path")"
if [ ! -f "${SR_WORKSPACE:-.}/$unit_dir/UNIT.md" ]; then
  refuse "$unit_dir has no UNIT.md, so it is not a unit yet. Write $unit_dir/UNIT.md first (frontmatter created/type/status/tags, the pitch grounded in the user's words; see the document-topic skill), then write $path."
fi
exit 0
