#!/usr/bin/env bash
# Shared by the unit-md-first gate and its file-guard: one library, two thin entries.
# Each entry reads the event kind and its own bytes; nothing that follows branches
# on it.

lib_init() {
set -euo pipefail

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1"; }

kind="$(field '.event.kind // ""')"
path="$(field '.event.path // ""')"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}
}

lib_check() {

[ -n "$path" ] || refuse "the event named no path, so this rule could not check it"
unit_dir="$(dirname "$path")"
if [ ! -f "${SR_WORKSPACE:-.}/$unit_dir/UNIT.md" ]; then
  refuse "$unit_dir has no UNIT.md, so it is not a unit yet. Write $unit_dir/UNIT.md first (frontmatter created/type/status/tags, the pitch grounded in the user's words; see the document-topic skill), then write $path."
fi
exit 0
}

check_lib_loaded=1
