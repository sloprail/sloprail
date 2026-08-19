#!/usr/bin/env bash
# A file carrying an sr:moved-from marker must be byte-identical to its origin
# at the pinned commit, minus the declared exceptions (import lines,
# whitespace) — the reconciliation unit 12 describes. Receives CheckPayload;
# the marker's fqn carries <path>@<sha>:<start>-<end>.
#
# This only runs inside the refactoring mode (the match saw to that), so it can
# assume it is looking at a declared move, not an incidental marker.
set -uo pipefail

input="$(cat)"
new="$(printf '%s' "$input" | jq -r 'if .event | has("newContent") then .event.newContent else null end')"
markers="$(printf '%s' "$input" | jq -c '.event.newMarkers // []')"

if [ "$new" = "null" ]; then
  exit 0
fi

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
{"decision":"block","reason":"moved-from origin '$fqn' names a commit or path this checkout does not have — a move cannot be verified against bytes that are not here."}
EOF
  exit 1
}

# Reconcile: drop import lines and normalize whitespace on both sides before
# comparing, so a legitimate import rewrite is not read as slop (unit 12's
# exception rules).
normalize() { grep -vE '^\s*(import|from)\b' | sed 's/[[:space:]]\+/ /g;s/^ //;s/ $//'; }
moved_body="$(printf '%s' "$new" | grep -vE '^\s*//\s*sr:' | normalize)"
origin_body="$(printf '%s' "$origin" | normalize)"

if [ "$moved_body" != "$origin_body" ]; then
  cat <<EOF
{"decision":"block","reason":"Content marked moved-from '$fqn' does not reconcile against its origin — after dropping imports and whitespace, the bytes differ. A move must carry the origin's bytes, not regenerated ones."}
EOF
  exit 1
fi

exit 0
