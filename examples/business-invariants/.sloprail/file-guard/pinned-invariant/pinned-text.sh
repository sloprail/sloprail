#!/usr/bin/env bash
# prepare: hand the judge the exact spec text each sr:invariant marker pins, so
# the judge rules on it without reading anything itself. A judge runs with this
# rule's folder as its working directory; a spec at the repository root is
# outside it, and asking the judge to fetch the pin costs a round of permission
# denials before it finds a way in. pin-still-matches-head.sh runs first and
# refuses a pin that does not resolve; this reads the pin with the same checks
# (pin.sh) rather than trusting that it ran, so a judge is never handed an empty
# <pinned> to rule against.
#
# Reads only the markers from the event, never the file's content, so it needs
# no per-kind resultKnown dispatch: a marker list is what the settled file (or,
# for a delete under `deletions: include`, the file it replaced) carried.
set -uo pipefail

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

# shellcheck source=pin.sh
. "${SR_GUARDRAIL_DIR:-.}/pin.sh" || refuse "pin.sh, which reads a pin, is missing beside this prepare."

input="$(cat)"

# A delete carries its markers as oldMarkers; every other kind as newMarkers.
markers="$(printf '%s' "$input" | jq -c '
  [ (if ((.event.newMarkers // []) | length) > 0 then .event.newMarkers else (.event.oldMarkers // []) end)[]
    | select(.kind == "invariant") ]')" \
  || refuse "The check payload did not parse, so the pinned text could not be read for the judge."

pins='[]'
count="$(printf '%s' "$markers" | jq 'length')"
for ((i = 0; i < count; i++)); do
  fqn="$(printf '%s' "$markers" | jq -r ".[$i].fqn")"

  parse_pin "$fqn" || refuse "$pin_error"
  pin_lines "$pin_sha" \
    || refuse "Invariant marker '$fqn' pins no text the judge could be given: $pin_error."
  text="$pin_text"

  # The whole spec as it stands at HEAD, for context: a pin range drawn too
  # narrowly can match byte-for-byte while the wording around it moved.
  if ! current="$(git -C "$pin_repo" cat-file blob "HEAD:$pin_path" 2>/dev/null)"; then
    refuse "Invariant marker '$fqn' names a path that no longer exists at HEAD, so the current spec could not be read for the judge."
  fi

  pins="$(jq -c --arg fqn "$fqn" --arg path "$pin_path" --arg lines "$pin_start-$pin_end" \
    --arg text "$text" --arg current "$current" \
    '. + [{fqn: $fqn, path: $path, lines: $lines, text: $text, current: $current}]' <<<"$pins")"
done

jq -n --argjson pins "$pins" '{additionalContext: {pins: $pins}}'
