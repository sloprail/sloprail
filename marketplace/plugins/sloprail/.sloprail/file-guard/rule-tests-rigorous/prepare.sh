#!/usr/bin/env bash
# Hands the judge an INDEX OF PATHS, not file contents: the files of the ONE rule under judgement (its
# declaration, README, scripts, templates) and, per case folder, every file of the case, all tree-relative
# paths read from SR_TREE (the judge's workspace is that snapshot, so it opens them with its Read tool). Only
# the tiny, always-needed facts are inline: the rule's name, nature and event kind.
# What this reads beyond the subject's files is in the subject's fingerprint (subjects.sh), so a changed case
# or a changed rule judges the rule again.
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
  echo "rule-tests-rigorous prepare: SR_TREE is unset, so the cases cannot be read. Refusing." >&2
  exit 1
}
rule_dir="$(printf '%s' "$payload" | jq -r '.subject.id // ""')" || rule_dir=""
if [ -z "$rule_dir" ] || [ ! -d "$SR_TREE/$rule_dir" ]; then
  echo '{"skip": true}'
  exit 0
fi
rule_split "$rule_dir" || {
  echo "rule-tests-rigorous prepare: $rule_dir is not a rule folder. Refusing." >&2
  exit 1
}
if [ -z "$(rule_cases "$rule_dir")" ]; then
  # no case stands: nothing to judge
  echo '{"skip": true}'
  exit 0
fi
owner_name="$(owner_rule "$CASE_ROOT" "$CASE_RULE")"
kind="$(owner_kind "$CASE_NATURE")"

# paths_json <abs file paths on stdin> -> a JSON array of tree-relative paths
paths_json() {
  local f
  while IFS= read -r f; do
    [ -n "$f" ] && printf '%s\n' "${f#"$SR_TREE"/}"
  done | jq -R . | jq -sc .
}

rule_files="$(owner_files "$CASE_ROOT" "$CASE_NATURE" "$CASE_RULE" | paths_json)"
cases='[]'
while IFS= read -r c; do
  [ -n "$c" ] || continue
  files="$(find "$SR_TREE/$c" -type f | LC_ALL=C sort | paths_json)"
  cases="$(printf '%s' "$cases" | jq -c --arg d "$c" --argjson f "$files" '. + [{dir: $d, files: $f}]')"
done < <(rule_cases "$rule_dir")

jq -n --arg dir "$rule_dir" --arg name "$owner_name" --arg nature "$CASE_NATURE" --arg kind "$kind" \
  --argjson rfiles "$rule_files" --argjson cases "$cases" \
  '{additionalContext: {rule: {dir: $dir, name: $name, nature: $nature, kind: $kind, files: $rfiles}, cases: $cases}}'
