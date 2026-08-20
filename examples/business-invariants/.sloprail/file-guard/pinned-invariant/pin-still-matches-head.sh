#!/usr/bin/env bash
# An sr:invariant marker's fqn carries a pinned spec reference:
#   <repo>@<sha>:<path>#L<start>-<end>
# Two things a script can settle before any judge runs:
#   1. the link resolves — the sha, path and line range are all real
#   2. the pinned range still matches HEAD — the spec has not moved since
set -uo pipefail

input="$(cat)"
markers="$(printf '%s' "$input" | jq -c '.event.newMarkers // .event.oldMarkers // []')"

fail() {
  cat <<EOF
{"decision":"block","reason":"$1"}
EOF
  exit 1
}

invariant_markers="$(printf '%s' "$markers" | jq -c '[.[] | select(.kind == "invariant")]')"
count="$(printf '%s' "$invariant_markers" | jq 'length')"

for i in $(seq 0 $((count - 1))); do
  fqn="$(printf '%s' "$invariant_markers" | jq -r ".[$i].fqn")"

  # <repo>@<sha>:<path>#L<start>-<end>
  repo="${fqn%%@*}"
  rest="${fqn#*@}"
  sha="${rest%%:*}"
  rest="${rest#*:}"
  path="${rest%%#*}"
  range="${rest#*#L}"
  start="${range%-*}"
  end="${range#*-}"

  if [ -z "$repo" ] || [ -z "$sha" ] || [ -z "$path" ] || [ -z "$start" ] || [ -z "$end" ]; then
    fail "Invariant marker '$fqn' does not parse as <repo>@<sha>:<path>#L<start>-<end>."
  fi

  pinned="$(git -C "$repo" show "$sha:$path" 2>/dev/null | sed -n "${start},${end}p")" \
    || fail "Invariant marker '$fqn' names a commit or path this checkout does not have."

  head_text="$(git -C "$repo" show "HEAD:$path" 2>/dev/null | sed -n "${start},${end}p")" \
    || fail "Invariant marker '$fqn' names a path that no longer exists at HEAD."

  if [ "$pinned" != "$head_text" ]; then
    fail "Invariant marker '$fqn' is pinned to text that has since changed at HEAD — the spec moved and the marker did not. Re-pin after confirming the code still upholds the current wording."
  fi
done

exit 0
