#!/usr/bin/env bash
# A file carrying an sr:moved-from marker must be byte-identical to its origin at
# the pinned commit, minus imports and whitespace. The marker's fqn carries
# <path>@<sha>:<start>-<end>.
set -uo pipefail

input="$(cat)"

# resultKnown, not has("newContent") — newContent is ALWAYS a present key on
# PreFileCreate/PreFileUpdate (the flat-event-fields discipline), so
# has("newContent") is always true and reading an absent value as "" is
# indistinguishable from a write that genuinely empties the file. The actual
# "this engine could not predict the result" signal is resultKnown, which was
# never consulted. This guard is preventive, so the engine's own dispatch
# already refuses any underivable Pre write before this script ever runs
# (services/sr-session/nature_fileguard.go's isUnderivablePreWrite) — this
# check is real defense in depth, not the only line of defense, but a check
# script must not trust its caller unconditionally.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" != "true" ]; then
      exit 0
    fi
    ;;
esac

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
