#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, a failing case. The base holds a legacy gate with no case (it must NOT be refused for
# that) and a demo gate with one passing case. The range edits only that case so that it fails: the rule's
# cases run (`sr-test run . --rule gate/demo`), and the refusal names its subject, its status and the tail of its output.
# Fixing the case (the same file, passing) passes the net range, the near neighbour of the refused change.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/file-guard
mkdir -p .sloprail/gate/legacy .sloprail/gate/demo/tests/the-case
cat "$F/legacy-gate.yaml" > .sloprail/gate/legacy/gate.yaml
cat "$F/legacy-check.txt" > .sloprail/gate/legacy/refuse.sh
cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
cat "$F/demo-refuse.txt" > .sloprail/gate/demo/refuse.sh
cat "$F/passing.txt" > .sloprail/gate/demo/tests/the-case/test.sh
chmod +x .sloprail/gate/demo/tests/the-case/test.sh .sloprail/gate/demo/refuse.sh .sloprail/gate/legacy/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a project with a case"
BASE=$(git rev-parse HEAD)

cat "$F/failing.txt" > .sloprail/gate/demo/tests/the-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "break the case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("gate/demo:the-case: fail")) and (.[0].reason|contains("the demo asserts 1 == 2")) and (.[0].reason|contains("legacy")|not)' "$SR_EVENTS_FILE" >/dev/null

# the recovery: the case is fixed and passes
cat "$F/fixed.txt" > .sloprail/gate/demo/tests/the-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "restore the case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==2 and .[1].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
