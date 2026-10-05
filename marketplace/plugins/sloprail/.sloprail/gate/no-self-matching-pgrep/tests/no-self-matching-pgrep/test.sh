#!/usr/bin/env bash
set -euo pipefail

git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "wait for the run")
# t1 refused for its own reason; the neighbours that change ONE thing each are permitted: another pattern with -f
# (t3), the sloprail pattern without -f (t4), and a different program without -f (t2)
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/no-self-matching-pgrep")] | length==4 and .[0].outcome=="refused" and .[0].tool_use_id=="t1" and (.[0].reason|contains("pgrep -f matches its own shell")) and .[1].outcome=="permitted" and .[1].tool_use_id=="t2" and .[2].outcome=="permitted" and .[2].tool_use_id=="t3" and .[3].outcome=="permitted" and .[3].tool_use_id=="t4"' >/dev/null
# the recovery the reason names: the sloprail command in the foreground (t5), and waited on by its PID (t6); the
# gate does not wake for either (no pgrep), and both ran
SESSION=$(echo "$RESULT" | jq -er .session)
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/no-self-matching-pgrep" and (.tool_use_id=="t5" or .tool_use_id=="t6"))] | length==0' >/dev/null
jq -es '[.[]|select(.type=="user")|.message.content[]?|select(.type=="tool_result" and (.tool_use_id=="t5" or .tool_use_id=="t6"))] | length==2 and all(.[]; .is_error!=true)' "$SESSION" >/dev/null
