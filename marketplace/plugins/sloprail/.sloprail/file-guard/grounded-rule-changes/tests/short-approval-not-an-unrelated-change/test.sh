#!/usr/bin/env bash
set -euo pipefail

# The same "lgtm" does not ground a change the assistant never proposed: it approved dropping PreCommandInvoke, and the
# commit drops Stop. The judge is handed the context, sees the approval covers something else, and the change is refused
# with its reasoning. The scripts are the sibling case's, copied (a case sees only its own folder); CHANGE picks the unrelated change.
CASES="$SR_TEST_CASE_DIR"
git init -q .
mkdir -p .sloprail/gate/demo
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
printf 'on:\n  - event: Stop\n  - event: PreCommandInvoke\n' > .sloprail/gate/demo/gate.yaml
git add .sloprail
git -c user.name=t -c user.email=t@t commit -q -m "a gate that already stands"
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/grounded-rule-changes/judge":"'"$CASES"'/judge-grounding.sh"}'
export CHANGE=unrelated
SID=short-approval-session
sr-test agent "$CASES/propose.sh" --session "$SID" --prompt "work on the demo gate" >/dev/null
RESULT=$(sr-test agent "$CASES/agent.sh" --session "$SID" --prompt "lgtm")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/grounded-rule-changes")|{kind,rule,outcome,on,tool_use_id,reason}' >&2; exit 1; }
SESSION=$(echo "$RESULT" | jq -er .session)
jq -rs '[.[]|select(.type=="user")|.message.content[]?|select(.type=="tool_result" and .tool_use_id=="c1")][0]|[.is_error, (.content|if type=="array" then map(.text)|join("") else . end)]|@json' "$SESSION" | jq -e '.[0]==true and (.[1]|contains("did not include removing Stop"))' >/dev/null || fail "c1: the unrelated change was not refused with the judge's reasoning"
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/grounded-rule-changes" and .on=="sr-checks run")] | map(.outcome)==["refused"]' >/dev/null || fail "grounded-rule-changes did not refuse the unrelated change"
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/grounded-rule-changes" and .outcome=="refused")][0] | (.reason|contains("did not include removing Stop"))' >/dev/null || fail "the logged refusal does not carry the judge's reasoning"
