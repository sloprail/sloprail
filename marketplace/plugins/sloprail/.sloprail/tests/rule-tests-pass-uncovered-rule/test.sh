#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, a rule nobody proves fires. The base holds a legacy gate with no case (it must NOT be
# refused for that). The range adds a new gate "demo" and an unrelated passing case: sr-test doctor shows the
# new gate uncovered, so the range is refused naming it. The agent adds a case whose agent makes the gate
# refuse and then permit, and the net range passes.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
mkdir -p .sloprail/file-guard .sloprail/gate/legacy .sloprail/gate/demo .sloprail/tests/unrelated .sloprail/tests/demo-case
cp -R "$SR_TEST_SLOPRAIL_DIR/file-guard/rule-tests-pass" .sloprail/file-guard/
chmod -R u+w .sloprail/file-guard
cat "$F/legacy-gate.yaml" > .sloprail/gate/legacy/gate.yaml
cat "$F/legacy-check.txt" > .sloprail/gate/legacy/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a project with a legacy gate"
BASE=$(git rev-parse HEAD)

cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
cat "$F/demo-refuse.txt" > .sloprail/gate/demo/refuse.sh
cat "$F/unrelated-case.txt" > .sloprail/tests/unrelated/test.sh
chmod +x .sloprail/gate/demo/refuse.sh .sloprail/tests/unrelated/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a new gate and an unrelated case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="rule-tests-pass")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("gate/demo")) and (.[0].reason|contains("no sr-test case exercises")) and (.[0].reason|contains("legacy")|not)' "$SR_EVENTS_FILE" >/dev/null

# the recovery: a case that makes the gate refuse and permit
cat "$F/demo-case-test.txt" > .sloprail/tests/demo-case/test.sh
cat "$F/demo-case-agent.txt" > .sloprail/tests/demo-case/agent.sh
chmod +x .sloprail/tests/demo-case/test.sh .sloprail/tests/demo-case/agent.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a case for the demo gate"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="rule-tests-pass")] | length==2 and .[1].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
