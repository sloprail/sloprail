#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, a failing case. The base holds a passing case and a legacy gate no case covers (it must NOT
# be refused for that). The range edits the case so that it fails: the refusal names the case, its status and
# the tail of its output. Fixing the case (the same file, passing) passes the net range, the near neighbour of the refused change.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
mkdir -p .sloprail/file-guard .sloprail/gate/legacy .sloprail/tests/the-case
cp -R "$SR_TEST_SLOPRAIL_DIR/file-guard/rule-tests-pass" .sloprail/file-guard/
chmod -R u+w .sloprail/file-guard
cat "$F/legacy-gate.yaml" > .sloprail/gate/legacy/gate.yaml
cat "$F/legacy-check.txt" > .sloprail/gate/legacy/refuse.sh
cat "$F/passing.txt" > .sloprail/tests/the-case/test.sh
chmod +x .sloprail/tests/the-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a project with a case"
BASE=$(git rev-parse HEAD)

cat "$F/failing.txt" > .sloprail/tests/the-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "break the case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="rule-tests-pass")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("the-case: fail")) and (.[0].reason|contains("the demo asserts 1 == 2")) and (.[0].reason|contains("legacy")|not)' "$SR_EVENTS_FILE" >/dev/null

# the recovery: the case is fixed and passes
cat "$F/fixed.txt" > .sloprail/tests/the-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "restore the case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="rule-tests-pass")] | length==2 and .[1].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
