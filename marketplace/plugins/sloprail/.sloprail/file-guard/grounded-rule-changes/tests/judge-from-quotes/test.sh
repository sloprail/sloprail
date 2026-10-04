#!/usr/bin/env bash
set -euo pipefail

# grounded-rule-changes judges a commit range at `sr-checks run`, deciding (judge mocked, from the quotes it is shown)
# whether the cited words ask for the change: a quote that is not about the change is refused with the judge's
# reasoning; a follow-up commit that really changes the file and cites the words that ask for it passes.
git init -q .
mkdir -p .sloprail/gate/demo
printf 'on:\n  - event: Stop\n  - event: PreFileWrite\n    match: "true"\n' > .sloprail/gate/demo/gate.yaml
git add .sloprail/gate/demo/gate.yaml
git -c user.name=t -c user.email=t@t commit -q -m "a gate that already stands"
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/grounded-rule-changes/judge":"'"$SR_TEST_CASE_DIR"'/judge-grounding.sh"}'
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "tighten the demo gate to run only on Stop. Also tell me the time.")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/grounded-rule-changes")|{kind,rule,outcome,on,tool_use_id,reason}' >&2; exit 1; }
SESSION=$(echo "$RESULT" | jq -er .session)
result_of() { jq -rs --arg id "$1" '[.[]|select(.type=="user")|.message.content[]?|select(.type=="tool_result" and .tool_use_id==$id)][0]|[.is_error, (.content|if type=="array" then map(.text)|join("") else . end)]|@json' "$SESSION"; }

echo "$(result_of c1)" | jq -e '.[0]==true and (.[1]|contains("not about the demo gate"))' >/dev/null || fail "c1: the off-topic quote was not refused with the judge's reasoning: $(result_of c1)"
echo "$(result_of c2)" | jq -e '.[0]!=true' >/dev/null || fail "c2: the cited follow-up was refused: $(result_of c2)"
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/grounded-rule-changes" and .on=="sr-checks run")] | map(.outcome)==["refused","passed"]' >/dev/null || fail "grounded-rule-changes outcomes are not refused, passed"
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/grounded-rule-changes" and .outcome=="refused")][0].reason | contains("not about the demo gate")' >/dev/null || fail "the logged refusal does not carry the judge's reasoning"
