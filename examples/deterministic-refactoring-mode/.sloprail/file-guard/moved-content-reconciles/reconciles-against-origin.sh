#!/usr/bin/env bash
# A file carrying an sr:moved-from marker must be byte-identical to its origin at
# the pinned commit, minus imports and whitespace. The marker's fqn carries
# <path>@<sha>:<start>-<end>.
#
# This is the STOP-TIME copy (the file-guard): it reads the settled bytes off a
# PostFileCreate/PostFileUpdate. A file-guard never sees a Pre event, so the
# Pre-only `resultKnown` field does not apply here; the settled-bytes equivalent
# is `newContentKnown`, handled below. The gate of the same name keeps the
# pre-write copy, which reads the pending bytes and consults `resultKnown`.
set -uo pipefail

input="$(cat)"

# newContentKnown is false when the settled bytes could not be read (a file that
# is not a regular file, past the byte budget); newContent is then "", the same
# string as an emptied file. A reconcile against bytes nobody read is no check, so
# refuse rather than permit.
known="$(printf '%s' "$input" | jq -r 'if (.event | has("newContentKnown")) then .event.newContentKnown else true end')"
if [ "$known" != "true" ]; then
  jq -n '{reason: "This file carries an sr:moved-from marker but its settled bytes could not be read, so it cannot be reconciled against its origin. Make it a regular file holding the moved content."}'
  exit 1
fi

new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
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
