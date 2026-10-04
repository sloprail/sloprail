#!/usr/bin/env bash
set -euo pipefail

# The committed-bytes half of authoring-slop, as an agent meets it through `sr-checks run`: first the grep
# (a hook reading newContent without resultKnown), then, once the grep passes, the judge (a hook that asks
# resultKnown and then admits everything). The judge is mocked and decides from the script it is shown.
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/authoring-slop/judge":"'"$SR_TEST_CASE_DIR"'/judge-slop.sh","sloprail/file-guard/grounded-rule-changes/judge":"'"$SR_TEST_CASE_DIR"'/judge-pass.sh"}'
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "add a gate script")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/authoring-slop")|{kind,rule,outcome,on,tool_use_id,reason}' >&2; exit 1; }
SESSION=$(echo "$RESULT" | jq -er .session)
result_of() { jq -rs --arg id "$1" '[.[]|select(.type=="user")|.message.content[]?|select(.type=="tool_result" and .tool_use_id==$id)][0]|[.is_error, (.content|if type=="array" then map(.text)|join("") else . end)]|@json' "$SESSION"; }

# 1. the grep refuses the committed shape, naming the rule and the fix
echo "$(result_of c1)" | jq -e '.[0]==true and (.[1]|contains("content-may-be-unresolvable") and contains("resultKnown"))' >/dev/null || fail "c1: sr-checks run did not refuse the committed shape with its rule and fix: $(result_of c1)"
# 2. the grep passes the fixed shape; the judge refuses the inert hook with its reasoning
echo "$(result_of c2)" | jq -e '.[0]==true and (.[1]|contains("never refuses anything"))' >/dev/null || fail "c2: the judge's refusal did not reach the agent: $(result_of c2)"
echo "$(result_of c2)" | jq -e '.[1]|contains("content-may-be-unresolvable")|not' >/dev/null || fail "c2: the grep still refused"
# 3. the hook with a refusal path passes
echo "$(result_of c3)" | jq -e '.[0]!=true' >/dev/null || fail "c3: the corrected hook was refused: $(result_of c3)"
# the log shows the same three decisions
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/authoring-slop" and .on=="sr-checks run")] | map(.outcome)==["refused","refused","passed"]' >/dev/null || fail "FileGuardChecked outcomes are not refused, refused, passed"
# the logged refusals carry their reasons: the grep's own rule, then the judge's reasoning
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/authoring-slop" and .outcome=="refused")] | (.[0].reason|contains("content-may-be-unresolvable")) and (.[1].reason|contains("never refuses anything"))' >/dev/null || fail "the logged refusals do not carry the grep rule and the judge reasoning"
# the PreFileWrite gate (gate/authoring-slop): the slop shape written with the Write tool is refused before it
# lands, naming the rule and resultKnown; the same hook asking resultKnown first is permitted
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/authoring-slop" and .tool_use_id=="gw1")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("content-may-be-unresolvable") and contains("resultKnown"))' >/dev/null ||
  fail "gw1: the gate did not refuse the slop-shaped Write with its rule and resultKnown"
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/authoring-slop" and .tool_use_id=="gw2")] | length==1 and .[0].outcome=="permitted"' >/dev/null ||
  fail "gw2: the gate refused the hook that asks resultKnown first"
grep -q resultKnown .sloprail/gate/demo/second.sh || fail "second.sh on disk is not the permitted hook"
