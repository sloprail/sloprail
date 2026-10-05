#!/usr/bin/env bash
set -euo pipefail

# A short approval is read against what it answered. The assistant proposed dropping the demo gate's PreCommandInvoke
# trigger, the user said "lgtm", and the commit cites "lgtm" (the only authority; the assistant message is context the
# judge is handed beside it): the change that was proposed is grounded and permitted.
# The judge is a mock that reads the record from the source path:line it is handed, so this proves the handoff and the
# verdict path; how the real model reads the rubric is for sr-eval.
git init -q .
mkdir -p .sloprail/gate/demo
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
printf 'on:\n  - event: Stop\n  - event: PreCommandInvoke\n' > .sloprail/gate/demo/gate.yaml
git add .sloprail
git -c user.name=t -c user.email=t@t commit -q -m "a gate that already stands"
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/grounded-rule-changes/judge":"'"$SR_TEST_CASE_DIR"'/judge-grounding.sh"}'
SID=short-approval-session
sr-test agent "$SR_TEST_CASE_DIR/propose.sh" --session "$SID" --prompt "work on the demo gate" >/dev/null
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --session "$SID" --prompt "lgtm")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/grounded-rule-changes")|{kind,rule,outcome,on,tool_use_id,reason}' >&2; exit 1; }
SESSION=$(echo "$RESULT" | jq -er .session)
jq -rs '[.[]|select(.type=="user")|.message.content[]?|select(.type=="tool_result" and .tool_use_id=="c1")][0]|.is_error' "$SESSION" | grep -qv true || fail "c1: the approved change was not permitted"
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/grounded-rule-changes" and .on=="sr-checks run")] | map(.outcome)==["passed"]' >/dev/null || fail "grounded-rule-changes did not pass the approved change"
