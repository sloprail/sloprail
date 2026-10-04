#!/usr/bin/env bash
# One subject per changed case folder. id and files are the case; the fingerprint is everything else the
# verdict depends on: the case's whole folder and the rules it covers (read from SR_TREE).
set -uo pipefail
dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
. "$dir/lib.sh"

payload="$(cat)"
if [ -z "${SR_TREE:-}" ]; then
  echo "rule-tests-rigorous subjects: SR_TREE is unset, so the case cannot be read. Refusing: a rule that could not be checked has not permitted." >&2
  exit 1
fi
printf '%s' "$payload" | jq -e '.changeset.files | type == "array"' >/dev/null || {
  echo "rule-tests-rigorous subjects: the changeset could not be read." >&2
  exit 1
}

# the case folder of every changed file; a file straight under tests/ has none and is no case
cases="$(printf '%s' "$payload" | jq -r '.changeset.files[].path | capture("^(?<c>(.*/)?[.]sloprail/tests/[^/]+)/.") | .c' | LC_ALL=C sort -u)"

out='[]'
while IFS= read -r c; do
  [ -n "$c" ] || continue
  files="$(printf '%s' "$payload" | jq -c --arg c "$c" '[.changeset.files[] | select(.path | startswith($c + "/")) | .path]')"
  abs=("$SR_TREE/$c")
  root="$(root_abs "$c")"
  while IFS= read -r r; do
    [ -n "$r" ] || continue
    abs+=("$root/.sloprail/$r")
  done < <(covered_rules "$c")
  fp="$(tree_sha "${abs[@]}")"
  out="$(printf '%s' "$out" | jq -c --arg c "$c" --argjson f "$files" --arg fp "$fp" '. + [{id: $c, files: $f, fingerprint: $fp}]')"
done <<<"$cases"
printf '%s\n' "$out"
