#!/usr/bin/env bash
# A file carrying an sr:moved-from marker must be byte-identical to its origin at
# the pinned commit, minus imports and whitespace. The marker's fqn carries
# <path>@<sha>:<start>-<end>.
#
# This is the PRE-WRITE copy (the gate): it reads the pending bytes off a
# PreFileCreate/PreFileUpdate. The file-guard of the same name keeps the Stop-time
# copy, which reads the settled bytes.
set -uo pipefail

input="$(cat)"

# resultKnown, not has("newContent") — newContent is ALWAYS a present key on
# PreFileCreate/PreFileUpdate, so has("newContent") is always true and an absent
# value reads as "", indistinguishable from a write that genuinely empties the
# file. The "engine could not predict the result" signal is resultKnown. A gate
# does not fail closed on it by itself, and a reconcile against bytes nobody saw
# would be no check at all, so refuse: a write whose result cannot be computed
# (sed -i, an unresolvable sr-file line) is not admitted.
known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
if [ "$known" != "true" ]; then
  jq -n '{reason: "This write carries an sr:moved-from marker but the bytes it would leave in the file cannot be worked out from the command, so it cannot be reconciled against its origin. Write the moved content directly (the whole file) instead of editing it in place."}'
  exit 1
fi

new="$(printf '%s' "$input" | jq -r '.event.newContent')"
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
