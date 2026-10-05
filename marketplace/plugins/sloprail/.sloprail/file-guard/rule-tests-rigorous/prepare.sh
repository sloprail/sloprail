#!/usr/bin/env bash
# Hands the judge the whole committed case folder (every file, not only the changed ones) and the files of
# the ONE rule that owns it (the folder the case sits in; its own cases excluded), both read from SR_TREE.
# What this reads beyond the subject's files is in the subject's fingerprint (subjects.sh), so a changed
# owner rule judges its cases again.
set -uo pipefail
lib_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
unset rule_tests_rigorous_lib_loaded
. "$lib_dir/lib.sh" || exit 2
[ "${rule_tests_rigorous_lib_loaded:-}" = 1 ] || exit 2

payload="$(cat)"
command -v jq >/dev/null 2>&1 || {
  echo "rule-tests-rigorous prepare: needs jq, which is not on PATH. Refusing: a check that cannot run has not approved." >&2
  exit 1
}
[ -n "${SR_TREE:-}" ] || {
  echo "rule-tests-rigorous prepare: SR_TREE is unset, so the case cannot be read. Refusing." >&2
  exit 1
}
case_dir="$(printf '%s' "$payload" | jq -r '.subject.id // ""')" || case_dir=""
if [ -z "$case_dir" ] || [ ! -d "$SR_TREE/$case_dir" ]; then
  echo '{"skip": true}'
  exit 0
fi

# dir_json <abs dir> <label prefix> -> [{path, content}] for every file under it, sorted, text only
dir_json() {
  local d="$1" prefix="$2" f out='[]'
  while IFS= read -r f; do
    [ -f "$f" ] || continue
    out="$(printf '%s' "$out" | jq -c --arg p "$prefix${f#"$d"/}" --rawfile c "$f" '. + [{path: $p, content: $c}]')"
  done < <(find "$d" -type f | LC_ALL=C sort)
  printf '%s' "$out"
}

case_files="$(dir_json "$SR_TREE/$case_dir" "")"
case_split "$case_dir" || {
  echo "rule-tests-rigorous prepare: $case_dir is not a case folder of a rule. Refusing." >&2
  exit 1
}
owner_name="$(owner_rule "$CASE_ROOT" "$CASE_RULE")"
kind="$(owner_kind "$CASE_NATURE")"
rule_files='[]'
while IFS= read -r f; do
  [ -n "$f" ] || continue
  rule_files="$(printf '%s' "$rule_files" | jq -c --arg p "${f#"$(root_abs "$CASE_ROOT")"/.sloprail/}" --rawfile c "$f" '. + [{path: $p, content: $c}]')"
done < <(owner_files "$CASE_ROOT" "$CASE_NATURE" "$CASE_RULE")

jq -n --arg dir "$case_dir" --argjson files "$case_files" --arg name "$owner_name" --arg nature "$CASE_NATURE" \
  --arg kind "$kind" --argjson rfiles "$rule_files" \
  '{additionalContext: {case: {dir: $dir, files: $files}, rule: {name: $name, nature: $nature, kind: $kind, files: $rfiles}}}'
