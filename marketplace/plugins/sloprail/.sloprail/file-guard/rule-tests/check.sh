#!/usr/bin/env bash
# Runs `sr-checks doctor` on the one rule this subject is (id `<nature>/<name>`), over the rule as
# committed at head (SR_TREE). Refuses when the rule does not load, lacks a refuse or a permit
# case, stubs a judge only one way, or has a failing case; the refusal carries the doctor's own
# report. Rollout: see the sibling README.md. Fails closed: whatever could not be checked is
# refused.
set -uo pipefail
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset rule_tests_lib_loaded
. "$lib_dir/rule-tests-lib.sh" || exit 2
[ "${rule_tests_lib_loaded:-}" = 1 ] || exit 2

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

command -v jq >/dev/null 2>&1 || {
  echo "rule-tests needs jq on PATH, so the rules this range changes could not be tested" >&2
  exit 1
}
payload="$(cat)"
[ "$(printf '%s' "$payload" | jq -r '.event.kind // ""')" = "Changeset" ] ||
  refuse "expected a Changeset event, so the rules this range changes could not be tested"
id="$(printf '%s' "$payload" | jq -r '.subject.id // ""')" || id=""
nature="${id%%/*}"
name="${id#*/}"
case "$nature" in file-guard | gate | context) ;; *) refuse "the subject '$id' is not a rule (<nature>/<name>), so it could not be tested" ;; esac
[ -n "$name" ] && [ "$name" != "$id" ] || refuse "the subject '$id' names no rule, so it could not be tested"
[ -n "${SR_TREE:-}" ] && [ -d "$SR_TREE" ] || refuse "no snapshot of the commit (SR_TREE) to read $id from, so it could not be tested"
[ -n "${SR_WORKSPACE:-}" ] || refuse "the project's repository (SR_WORKSPACE) is unknown, so the history of $id could not be read"
command -v sr-checks >/dev/null 2>&1 || refuse "sr-checks is not on PATH, so the tests of $id could not be run (install sloprail's binaries together)"

base="$(printf '%s' "$payload" | jq -r '.changeset.base // ""')" || base=""

# The rule was removed: there is nothing left to prove. (Removing what stood is a change
# grounded-rule-changes judges.)
[ -f "$SR_TREE/.sloprail/$id/$nature.yaml" ] || exit 0

# The rollout. A rule that did not stand at the base is NEW and must come with its tests. One
# that stood but had no tests is grandfathered until its first case: it is checked only if it
# has any (a failing case still refuses; missing coverage does not). One that had tests keeps
# them: deleting its cases is no way out.
at_rev "$base" ".sloprail/$id/$nature.yaml"
case $? in
  0) standing=1 ;;
  1) standing=0 ;;
  *) refuse "the project's repository could not be read, so whether $id is a new rule could not be told" ;;
esac
at_rev "$base" ".sloprail/$id/tests"
case $? in
  0) tested=1 ;;
  1) tested=0 ;;
  *) refuse "the project's repository could not be read, so whether $id had tests could not be told" ;;
esac
allow=""
if [ "$standing" = 1 ] && [ "$tested" = 0 ]; then
  allow="--allow-untested"
fi

# The doctor runs the rule's cases in throwaway sandboxes of its own; it reads the rule from the
# snapshot and touches nothing here.
report="$(cd "$SR_TREE" && sr-checks doctor --rules-dir "$SR_TREE/.sloprail" $allow "$id" 2>&1)"
rc=$?
[ "$rc" = 0 ] && exit 0

# Clipped: the head of the report names the rule and what is missing; the case lines follow.
report="$(printf '%s' "$report" | head -c 3500)"
refuse "the rule $id is not proved by its tests. Fix the rule or its cases under .sloprail/$id/tests/ (see the authoring-guardrails skill, testing.md), then commit; \`sr-checks doctor $id\` reproduces this.
$report"
