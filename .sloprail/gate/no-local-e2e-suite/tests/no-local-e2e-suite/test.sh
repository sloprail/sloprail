#!/usr/bin/env bash
set -euo pipefail

git init -q -b main .
mkdir -p .sloprail
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "run the e2e tests")
G='.events[]|select(.kind=="GateChecked" and .rule=="no-local-e2e-suite")'
# refused: (a) `...`, (b) more than 2 packages, (c) no -run, make targets, and the wrapped forms
for id in c1 c2 c3 c4 c5 c6 c7 c8; do
  echo "$RESULT" | jq -e --arg id "$id" "[$G | select(.tool_use_id==\$id)] | length==1 and .[0].outcome==\"refused\" and (.[0].reason|contains(\"Push the branch and let CI run it\"))" >/dev/null
done
# permitted: a named test, 2 packages with -run, unit packages, env-wrapped -test.run
for id in p1 p2 p3 p4; do
  echo "$RESULT" | jq -e --arg id "$id" "[$G | select(.tool_use_id==\$id)] | map(select(.outcome==\"permitted\")) | length==1" >/dev/null
done
