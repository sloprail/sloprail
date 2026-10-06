#!/usr/bin/env bash
set -euo pipefail

git init -q -b main .
mkdir -p .sloprail
# the gate ships off: this project opts in
printf 'enabled:\n  - sloprail/gate/no-subagent-check-runs\ndisabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "judge the range")
G='[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/no-subagent-check-runs")]'
# inside the sub-agent both spellings of a judge run are refused for this gate's reason; verify never wakes the gate
echo "$RESULT" | jq -e "$G"' | map(select(.tool_use_id=="s1" or .tool_use_id=="s2")) | length==2 and all(.[]; .outcome=="refused" and (.reason|contains("Sub-agents do not run")))' >/dev/null
echo "$RESULT" | jq -e "$G"' | map(select(.tool_use_id=="s3")) | length==0' >/dev/null
# the same command from the main session is permitted
echo "$RESULT" | jq -e "$G"' | map(select(.tool_use_id=="t2")) | length==1 and .[0].outcome=="permitted"' >/dev/null
