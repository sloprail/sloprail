#!/usr/bin/env bash
# prepare: hand the judge the exact spec text each sr:invariant marker pins, so
# the judge rules on it without reading anything itself. A judge runs with this
# rule's folder as its working directory; a spec at the repository root is
# outside it, and asking the judge to fetch the pin costs a round of permission
# denials before it finds a way in. The pin was already confirmed to resolve and
# to match HEAD by pin-still-matches-head.sh, which runs first.
#
# Reads only the markers from the event, never the file's content, so it needs
# no per-kind resultKnown dispatch: a marker list is what the settled file (or,
# for a delete under `deletions: include`, the file it replaced) carried.
set -uo pipefail

input="$(cat)"

# A delete carries its markers as oldMarkers; every other kind as newMarkers.
markers="$(printf '%s' "$input" | jq -c '
  [ (if ((.event.newMarkers // []) | length) > 0 then .event.newMarkers else (.event.oldMarkers // []) end)[]
    | select(.kind == "invariant") ]')"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

pins='[]'
count="$(printf '%s' "$markers" | jq 'length')"
for i in $(seq 0 $((count - 1))); do
  fqn="$(printf '%s' "$markers" | jq -r ".[$i].fqn")"

  # <repo>@<sha>:<path>#L<start>-<end> — the same parse the pin check uses.
  repo="${fqn%%@*}"
  rest="${fqn#*@}"
  sha="${rest%%:*}"
  rest="${rest#*:}"
  path="${rest%%#*}"
  range="${rest#*#L}"
  start="${range%-*}"
  end="${range#*-}"

  if ! text="$(git -C "$repo" show "$sha:$path" 2>/dev/null)"; then
    refuse "Invariant marker '$fqn' names a commit or path this checkout does not have, so its pinned text could not be read for the judge."
  fi
  text="$(printf '%s\n' "$text" | sed -n "${start},${end}p")"
  # The whole spec as it stands at HEAD, for context: a pin range drawn too
  # narrowly can match byte-for-byte while the wording around it moved.
  if ! current="$(git -C "$repo" show "HEAD:$path" 2>/dev/null)"; then
    refuse "Invariant marker '$fqn' names a path that no longer exists at HEAD, so the current spec could not be read for the judge."
  fi

  pins="$(jq -c --arg fqn "$fqn" --arg path "$path" --arg lines "$start-$end" \
    --arg text "$text" --arg current "$current" \
    '. + [{fqn: $fqn, path: $path, lines: $lines, text: $text, current: $current}]' <<<"$pins")"
done

jq -n --argjson pins "$pins" '{additionalContext: {pins: $pins}}'
