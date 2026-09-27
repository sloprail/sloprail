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

# shellcheck source=pin.sh
. "${SR_GUARDRAIL_DIR:-.}/pin.sh" || fail "pin.sh, which reads a pin, is missing beside this check."

input="$(cat)"
markers="$(printf '%s' "$input" | jq -c '[(.event.newMarkers // .event.oldMarkers // [])[] | select(.kind == "invariant")]')" \
  || fail "The check payload did not parse, so the invariant pins could not be checked."

count="$(printf '%s' "$markers" | jq 'length')"
for ((i = 0; i < count; i++)); do
  fqn="$(printf '%s' "$markers" | jq -r ".[$i].fqn")"

  parse_pin "$fqn" || fail "$pin_error"

  pin_lines "$pin_sha" \
    || fail "Invariant marker '$fqn' names a commit or path this checkout does not have, or a range with no text in it: $pin_error. Pin it to the spec lines that state the rule, at a commit that has them."
  pinned="$pin_text"

  pin_lines HEAD \
    || fail "Invariant marker '$fqn' names a path or range that no longer exists at HEAD: $pin_error."

  if [ "$pinned" != "$pin_text" ]; then
    fail "Invariant marker '$fqn' is pinned to text that has since changed at HEAD — the spec moved and the marker did not. Re-pin after confirming the code still upholds the current wording."
  fi
done

exit 0
