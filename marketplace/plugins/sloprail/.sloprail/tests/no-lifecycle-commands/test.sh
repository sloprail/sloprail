#!/usr/bin/env bash
set -euo pipefail

git init -q .
RESULT=$(sr-test agent "$CASE/agent.sh" --prompt "check the session")
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/no-lifecycle-commands")] | .[0].outcome=="refused" and .[0].tool_use_id=="t1" and (.[0].reason|contains("sr-checks run --base")) and .[1].outcome=="permitted" and .[1].tool_use_id=="t2"' >/dev/null
