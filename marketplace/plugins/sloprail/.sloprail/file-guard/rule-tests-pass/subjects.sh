#!/usr/bin/env bash
# One subject per `.sloprail` root the change touches. Its fingerprint is the rules plus the tests of that
# root: every file under its `.sloprail/`, read from SR_TREE (check.sh runs the root's cases).
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

# one subject per touched `.sloprail` root: its id is the root (a dir relative to the tree, "." for the repo
# root) and its files are the changed paths under that root's `.sloprail/`; the fingerprint is every file of
# that `.sloprail/` (the rules and the tests of the root), read from SR_TREE
paths="$(printf '%s' "$payload" | jq -r '.changeset.files[].path')"
out='[]'
while IFS= read -r r; do
  [ -n "$r" ] || continue
  if [ "$r" = "." ]; then dir="$SR_TREE/.sloprail"; else dir="$SR_TREE/$r/.sloprail"; fi
  files="$(printf '%s\n' "$paths" | jq -R -s -c --arg r "$r" '[split("\n")[] | select(length > 0) | select(if $r == "." then startswith(".sloprail/") else startswith($r + "/.sloprail/") end)]')"
  fp="$(tree_sha "$dir")"
  out="$(printf '%s' "$out" | jq -c --arg r "$r" --argjson f "$files" --arg fp "$fp" '. + [{id: $r, files: $f, fingerprint: $fp}]')"
done < <(printf '%s\n' "$paths" | roots_of)
printf '%s\n' "$out"
