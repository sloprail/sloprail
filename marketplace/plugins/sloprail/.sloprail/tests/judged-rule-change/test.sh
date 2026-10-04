#!/usr/bin/env bash
set -euo pipefail
# The authoring-slop judge is mocked to fail: a committed hook script under .sloprail/gate/ is refused at Stop
# with the judge's reasoning. Every judge the engine meets needs an entry while the variable is set.
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/authoring-slop/judge":"'"$SR_TEST_CASE_DIR"'/judge-fail.sh","sloprail/file-guard/grounded-rule-changes/judge":"'"$SR_TEST_CASE_DIR"'/judge-fail.sh"}'
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "add a hook script")
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/authoring-slop")] | length>=1 and .[0].outcome=="refused" and (.[0].reason|contains("guesses instead of reading the event"))' >/dev/null
