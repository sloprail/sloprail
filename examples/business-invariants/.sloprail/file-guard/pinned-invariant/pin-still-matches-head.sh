#!/usr/bin/env bash
# An sr:invariant marker's fqn carries a pinned spec reference:
#   <repo>@<sha>:<path>#L<start>-<end>
# Two things a script can settle before any judge runs:
#   1. the link resolves — the sha, path and line range are all real, and the
#      range holds text (pin.sh checks the fqn before git reads anything with it)
#   2. the pinned range still matches HEAD — the spec has not moved since
set -uo pipefail

fail() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

# A helper stopped early runs only partly (whether the `.` then fails depends
# on the bash version); only its last-line sentinel proves it loaded whole.
unset pin_loaded
# shellcheck source=pin.sh
. "${SR_GUARDRAIL_DIR:-.}/pin.sh" || fail "pin.sh, which reads a pin, is missing beside this check."
[ "${pin_loaded:-}" = 1 ] || fail "pin.sh did not load whole (its last-line sentinel pin_loaded is unset)."

input="$(cat)"
[ "$(printf '%s' "$input" | jq -r '.event.kind // ""' 2>/dev/null)" = "Changeset" ] \
  || fail "The check payload is not a Changeset, so the invariant pins could not be checked."

# The changeset's files: a deleted file carries its markers as oldMarkers, every
# other file as newMarkers. Every pin of every file is checked, against the range's
# head (the commit the changeset ends at), not against whatever HEAD is by then.
pins="$(printf '%s' "$input" | jq -c '
  [ .changeset.files[]
    | { path, fqns: [ (if .status == "D" then .oldMarkers else .newMarkers end)[]? | select(.kind == "invariant") | .fqn ] }
    | . as $f | $f.fqns[] | { path: $f.path, fqn: . } ]')" \
  || fail "The check payload did not parse, so the invariant pins could not be checked."
head_rev="${SR_HEAD:-HEAD}"

count="$(printf '%s' "$pins" | jq 'length')"
for ((i = 0; i < count; i++)); do
  fqn="$(printf '%s' "$pins" | jq -r ".[$i].fqn")"
  file="$(printf '%s' "$pins" | jq -r ".[$i].path")"

  parse_pin "$fqn" || fail "$file: $pin_error"

  pin_lines "$pin_sha" \
    || fail "$file: invariant marker '$fqn' names a commit or path this checkout does not have, or a range with no text in it: $pin_error. Pin it to the spec lines that state the rule, at a commit that has them."
  pinned="$pin_text"

  pin_lines "$head_rev" \
    || fail "$file: invariant marker '$fqn' names a path or range that no longer exists at HEAD: $pin_error."

  if [ "$pinned" != "$pin_text" ]; then
    fail "$file: invariant marker '$fqn' is pinned to text that has since changed at HEAD — the spec moved and the marker did not. Re-pin after confirming the code still upholds the current wording."
  fi
done

exit 0
