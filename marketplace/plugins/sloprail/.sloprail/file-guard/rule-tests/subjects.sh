#!/usr/bin/env bash
# One subject per rule the range touches: id `<nature>/<name>`, the changed files of that rule,
# and a fingerprint of everything the doctor's verdict depends on beyond them: the rule's whole
# folder as committed at head (git's tree hash), and whether the rule and its tests already
# stood at the base (the rollout's grandfathering reads that). Run by `run` and `verify` alike,
# with the Changeset on stdin and no session: it uses git only.
set -uo pipefail
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset rule_tests_lib_loaded
. "$lib_dir/rule-tests-lib.sh" || exit 2
[ "${rule_tests_lib_loaded:-}" = 1 ] || exit 2

fail() {
  echo "rule-tests: $1" >&2
  exit 1
}

command -v jq >/dev/null 2>&1 || fail "needs jq on PATH, so it could not tell which rules the range touches"
[ -n "${SR_WORKSPACE:-}" ] || fail "was not told the project's repository (SR_WORKSPACE), so it could not read the rules' history"
payload="$(cat)"
base="$(printf '%s' "$payload" | jq -r '.changeset.base // ""')" || fail "could not read the changeset"
head="$(printf '%s' "$payload" | jq -r '.changeset.head // ""')" || fail "could not read the changeset"
[ -n "$head" ] || fail "the changeset names no head, so the rules at head could not be read"
paths="$(printf '%s' "$payload" | jq -r '.changeset.files[].path')" || fail "could not read the changeset's files"

# `rule<TAB>path` for every changed path inside a rule's folder, grouped by rule.
pairs="$(
  while IFS= read -r p; do
    [ -n "$p" ] || continue
    r="$(rule_of "$p")"
    [ -n "$r" ] && printf '%s\t%s\n' "$r" "$p"
  done <<<"$paths" | sort
)"

out='[]'
current=""
files=""
emit() {
  [ -n "$current" ] || return 0
  local nature="${current%%/*}" tree standing tested rc
  tree="$(git -C "$SR_WORKSPACE" rev-parse -q --verify "$head:.sloprail/$current" 2>/dev/null)" || tree="gone"
  at_rev "$base" ".sloprail/$current/$nature.yaml"
  rc=$?
  [ "$rc" = 2 ] && fail "could not read the project's repository to see whether $current already stood"
  standing=$((1 - rc))
  at_rev "$base" ".sloprail/$current/tests"
  rc=$?
  [ "$rc" = 2 ] && fail "could not read the project's repository to see whether $current already had tests"
  tested=$((1 - rc))
  local one
  one="$(printf '%s' "$files" | jq -R . | jq -cs --arg id "$current" --arg fp "$tree:standing=$standing:tested=$tested" \
    '{id: $id, files: ., fingerprint: $fp}')" || fail "could not describe the subject for $current"
  out="$(printf '%s' "$out" | jq -c --argjson s "$one" '. + [$s]')" || fail "could not assemble the subjects"
}
while IFS="$(printf '\t')" read -r r p; do
  [ -n "$r" ] || continue
  if [ "$r" != "$current" ]; then
    emit
    current="$r"
    files=""
  fi
  files="${files}${p}
"
done <<<"$pairs"
emit
printf '%s\n' "$out"
exit 0
