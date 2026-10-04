#!/usr/bin/env bash
set -euo pipefail

git init -q .
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "wait for the run")
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/no-self-matching-pgrep")] | .[0].outcome=="refused" and .[0].tool_use_id=="t1" and (.[0].reason|contains("pgrep -f matches its own shell")) and .[1].outcome=="permitted" and .[1].tool_use_id=="t2"' >/dev/null
