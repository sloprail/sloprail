#!/usr/bin/env bash
set -euo pipefail

git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "push the work")
# the commit was never judged by sr-checks run, so the push is refused
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/verify-before-push")] | .[0].outcome=="refused" and .[0].tool_use_id=="t1"' >/dev/null
