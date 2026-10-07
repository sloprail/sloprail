#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, the stored verdict. The range breaks the demo gate's only case: `sr-checks run` refuses it
# naming the case. Fixed, `sr-checks run` passes and stores the pass; `sr-checks verify` (the Stop and CI path,
# which never runs a case) must then FIND that stored pass, even at a later head. It can only if the subject key is
# the same on every run: the snapshot sr-checks reads sits in a different temp dir each time, so a fingerprint that
# hashed absolute paths would never match and verify would stay red ("missing") however often the cases passed.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/file-guard .sloprail/gate/demo/tests/the-case
cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
cat "$F/demo-refuse.txt" > .sloprail/gate/demo/refuse.sh
cat "$F/passing.txt" > .sloprail/gate/demo/tests/the-case/test.sh
chmod +x .sloprail/gate/demo/tests/the-case/test.sh .sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a project with a case"
BASE=$(git rev-parse HEAD)

cat "$F/failing.txt" > .sloprail/gate/demo/tests/the-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "break the case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("gate/demo:the-case: fail")) and (.[0].reason|contains("the demo asserts 1 == 2"))' "$SR_EVENTS_FILE" >/dev/null

# the recovery: the case is fixed, run passes and stores the verdict
cat "$F/fixed.txt" > .sloprail/gate/demo/tests/the-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "restore the case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==2 and .[1].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
# a later commit outside .sloprail/ changes the head's tree (so the two-trees lookup of a squash merge cannot
# find the pass), and the subject's files and the rule are as they were judged
printf 'unrelated\n' > unrelated.txt
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "an unrelated file"
# verify never runs a case: it reads the stored pass (the other rules of the plugin judge the demo case on their
# own and are not what is asserted here)
sr-checks verify --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass" and .on=="sr-checks verify")] | length==1 and .[0].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
