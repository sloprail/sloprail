#!/usr/bin/env bash
# Gate copy: refuse a PENDING write inside a unit folder whose UNIT.md does not
# exist yet. Path-based, not content-based, so an unresolvable pre-write result
# (resultKnown false) is still judged — nothing here reads newContent.
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
  PreFileCreate | PreFileUpdate) ;;
  *) refuse "unexpected event kind '$kind' for $path; this gate only judges pending file writes" ;;
esac

[ -n "$path" ] || refuse "the event named no path, so this rule could not check it"
unit_dir="$(dirname "$path")"
if [ ! -f "${SR_WORKSPACE:-.}/$unit_dir/UNIT.md" ]; then
  refuse "$unit_dir has no UNIT.md, so it is not a unit yet. Write $unit_dir/UNIT.md first (frontmatter created/type/status/tags, the pitch grounded in the user's words; see the document-topic skill), then write $path."
fi
exit 0
