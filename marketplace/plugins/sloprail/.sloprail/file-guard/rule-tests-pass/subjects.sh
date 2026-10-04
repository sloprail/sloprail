#!/usr/bin/env bash
# One subject for the whole change. Its fingerprint is the rules plus the tests: every file under the
# `.sloprail/` of each root the change touches, read from SR_TREE (check.sh runs all of their cases).
set -uo pipefail
lib_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
unset rule_tests_pass_lib_loaded
. "$lib_dir/lib.sh" || exit 2
[ "${rule_tests_pass_lib_loaded:-}" = 1 ] || exit 2

payload="$(cat)"
if [ -z "${SR_TREE:-}" ]; then
  echo "rule-tests-pass subjects: SR_TREE is unset, so the rules and tests cannot be read. Refusing: a rule that could not be checked has not permitted." >&2
  exit 1
fi
printf '%s' "$payload" | jq -e '.changeset.files | type == "array"' >/dev/null || {
  echo "rule-tests-pass subjects: the changeset could not be read." >&2
  exit 1
}

files="$(printf '%s' "$payload" | jq -c '[.changeset.files[].path]')"
dirs=()
while IFS= read -r r; do
  [ -n "$r" ] || continue
  if [ "$r" = "." ]; then dirs+=("$SR_TREE/.sloprail"); else dirs+=("$SR_TREE/$r/.sloprail"); fi
done < <(printf '%s' "$files" | jq -r '.[]' | roots_of)

fp="$(tree_sha "${dirs[@]}")"
jq -nc --argjson f "$files" --arg fp "$fp" '[{id: "changeset", files: $f, fingerprint: $fp}]'
