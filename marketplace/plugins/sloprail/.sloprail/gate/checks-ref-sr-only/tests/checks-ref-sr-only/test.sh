#!/usr/bin/env bash
set -euo pipefail

git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "move the checks ref")
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/checks-ref-sr-only")] | .[0].outcome=="refused" and .[0].tool_use_id=="t1" and (.[0].reason|contains("only sr-checks writes it")) and .[1].outcome=="permitted" and .[1].tool_use_id=="t2"' >/dev/null
# the recovery the reason names: after the refusal the agent reads the verdicts with `sr-checks show`. That is no
# git command naming the ref, so the gate does not wake for it (no event, never a refusal), and the call ran
SESSION=$(echo "$RESULT" | jq -er .session)
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/checks-ref-sr-only")] | length==2 and ([.[]|select(.tool_use_id=="t3")]|length)==0' >/dev/null
jq -es '[.[]|select(.type=="user")|.message.content[]?|select(.type=="tool_result" and .tool_use_id=="t3")] | length==1 and .[0].is_error!=true' "$SESSION" >/dev/null
