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
# Reads only the markers from the changeset, never a file's content: a marker list
# is what the committed file carried. A deleted file is skipped (below).
set -uo pipefail

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

# A helper stopped early runs only partly (whether the `.` then fails depends
# on the bash version); only its last-line sentinel proves it loaded whole.
unset pin_loaded
# shellcheck source=pin.sh
. "${SR_GUARDRAIL_DIR:-.}/pin.sh" || refuse "pin.sh, which reads a pin, is missing beside this prepare."
[ "${pin_loaded:-}" = 1 ] || refuse "pin.sh did not load whole (its last-line sentinel pin_loaded is unset)."

input="$(cat)"
[ "$(printf '%s' "$input" | jq -r '.event.kind // ""' 2>/dev/null)" = "Changeset" ] \
  || refuse "The check payload is not a Changeset, so the pinned text could not be read for the judge."

# A deleted file holds no code left to uphold anything, so there is nothing for
# the judge to rule on: skip it. pin-still-matches-head.sh has already checked the
# deleted file's pins, and whether the delete may drop them at all is
# pinned-spec-holds' question (it needs the user's words, unless another file
# carries the same pin).
pins_in="$(printf '%s' "$input" | jq -c '
  [ .changeset.files[]
    | select(.status != "D")
    | . as $f | ($f.newMarkers // [])[] | select(.kind == "invariant")
    | { path: $f.path, fqn: .fqn } ]')" \
  || refuse "The check payload did not parse, so the pinned text could not be read for the judge."

# Nothing left to judge (only deleted files in the range): abstain.
if [ "$(printf '%s' "$pins_in" | jq 'length')" -eq 0 ]; then
  printf '{"skip": true}\n'
  exit 0
fi

head_rev="${SR_HEAD:-HEAD}"
pins='[]'
count="$(printf '%s' "$pins_in" | jq 'length')"
for ((i = 0; i < count; i++)); do
  fqn="$(printf '%s' "$pins_in" | jq -r ".[$i].fqn")"
  file="$(printf '%s' "$pins_in" | jq -r ".[$i].path")"

  parse_pin "$fqn" || refuse "$file: $pin_error"
  pin_lines "$pin_sha" \
    || refuse "$file: invariant marker '$fqn' pins no text the judge could be given: $pin_error."
  text="$pin_text"

  # The whole spec as it stands at the range's head, for context: a pin range drawn
  # too narrowly can match byte-for-byte while the wording around it moved.
  if ! current="$(git -C "$pin_repo" cat-file blob "$head_rev:$pin_path" 2>/dev/null)"; then
    refuse "$file: invariant marker '$fqn' names a path that no longer exists at HEAD, so the current spec could not be read for the judge."
  fi

  pins="$(jq -c --arg file "$file" --arg fqn "$fqn" --arg path "$pin_path" --arg lines "$pin_start-$pin_end" \
    --arg text "$text" --arg current "$current" \
    '. + [{file: $file, fqn: $fqn, path: $path, lines: $lines, text: $text, current: $current}]' <<<"$pins")"
done

jq -n --argjson pins "$pins" '{additionalContext: {pins: $pins}}'
