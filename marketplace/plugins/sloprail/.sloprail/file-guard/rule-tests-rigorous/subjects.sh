#!/usr/bin/env bash
# One subject per changed case folder. id and files are the case; the fingerprint is everything else the
# verdict depends on: the case's whole folder and the files of the rule that owns it (read from SR_TREE).
# A case the range deleted is no subject: the rule's `deletions` default is skip, so a deleted file is never
# among the changed files, and a case folder only appears here while some file of it still stands.
set -uo pipefail
lib_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
unset rule_tests_rigorous_lib_loaded
. "$lib_dir/lib.sh" || exit 2
[ "${rule_tests_rigorous_lib_loaded:-}" = 1 ] || exit 2

payload="$(cat)"
if [ -z "${SR_TREE:-}" ]; then
  echo "rule-tests-rigorous subjects: SR_TREE is unset, so the case cannot be read. Refusing: a rule that could not be checked has not permitted." >&2
  exit 1
fi
printf '%s' "$payload" | jq -e '.changeset.files | type == "array"' >/dev/null || {
  echo "rule-tests-rigorous subjects: the changeset could not be read." >&2
  exit 1
}

# the case folder of every changed file: <nature>/<rule>/tests/<case> or file-guard/structure.tests/<case>
cases="$(printf '%s' "$payload" | jq -r '.changeset.files[].path | capture("^(?<c>(.*/)?[.]sloprail/((gate|file-guard|context)/[^/]+/tests|file-guard/structure[.]tests)/[^/]+)/.") | .c' | LC_ALL=C sort -u)"

out='[]'
while IFS= read -r c; do
  [ -n "$c" ] || continue
  case_split "$c" || continue
  files="$(printf '%s' "$payload" | jq -c --arg c "$c" '[.changeset.files[] | select(.path | startswith($c + "/")) | .path]')"
  fp="$({
    find "$SR_TREE/$c" -type f
    owner_files "$CASE_ROOT" "$CASE_NATURE" "$CASE_RULE"
  } | files_sha)"
  out="$(printf '%s' "$out" | jq -c --arg c "$c" --argjson f "$files" --arg fp "$fp" '. + [{id: $c, files: $f, fingerprint: $fp}]')"
done <<<"$cases"
printf '%s\n' "$out"
