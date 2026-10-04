#!/usr/bin/env bash
set -euo pipefail

git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "move the checks ref")
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/checks-ref-sr-only")] | .[0].outcome=="refused" and .[0].tool_use_id=="t1" and (.[0].reason|contains("only sr-checks writes it")) and .[1].outcome=="permitted" and .[1].tool_use_id=="t2"' >/dev/null
