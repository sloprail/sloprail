#!/usr/bin/env bash
# Shared by the moved-content-reconciles gate and its file-guard: one library, two thin entries.
# Each entry reads the event kind and its own bytes; nothing that follows branches
# on it.

lib_init() {
set -uo pipefail

input="$(cat)"
}

lib_check() {
markers="$(printf '%s' "$input" | jq -c '.event.newMarkers // []')"

fqn="$(printf '%s' "$markers" | jq -r '[.[] | select(.kind == "moved-from")][0].fqn // ""')"
if [ -z "$fqn" ]; then
  exit 0
fi

# <path>@<sha>:<start>-<end>
path="${fqn%@*}"; rest="${fqn#*@}"
sha="${rest%%:*}"; range="${rest#*:}"
start="${range%-*}"; end="${range#*-}"

origin="$(git show "$sha:$path" 2>/dev/null | sed -n "${start},${end}p")" || {
  cat <<EOF
{"reason":"moved-from origin '$fqn' names a commit or path this checkout does not have — a move cannot be verified against bytes that are not here."}
EOF
  exit 1
}

# Drop imports and normalize whitespace on both sides, so a legitimate import
# rewrite is not read as a rewrite of the moved code.
normalize() { grep -vE '^\s*(import|from)\b' | sed 's/[[:space:]]\+/ /g;s/^ //;s/ $//'; }
moved_body="$(printf '%s' "$new" | grep -vE '^\s*//\s*sr:' | normalize)"
origin_body="$(printf '%s' "$origin" | normalize)"

if [ "$moved_body" != "$origin_body" ]; then
  cat <<EOF
{"reason":"Content marked moved-from '$fqn' does not reconcile against its origin — after dropping imports and whitespace, the bytes differ. A move must carry the origin's bytes, not regenerated ones."}
EOF
  exit 1
fi

exit 0
}

reconciles_against_origin_lib_loaded=1
