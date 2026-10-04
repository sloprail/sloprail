#!/usr/bin/env bash
set -euo pipefail

# A declaration written one level too high from inside a script is invisible to the PreFileWrite gate.
# The agent judges its commits with `sr-checks run`, which refuses naming where the file belongs; it moves
# the file and commits, citing the user's words (the moved file is a rule change; move and commit as separate commands, as cite-before-commit demands); judging the
# same range again passes, and the Stop that follows blocks nothing.
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/grounded-rule-changes/judge":"'"$SR_TEST_CASE_DIR"'/judge-grounding.sh"}'
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "put the structure declaration in .sloprail, then move it where the engine reads it")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|{kind,rule,outcome,on,tool_use_id,reason}' >&2; exit 1; }
SESSION=$(echo "$RESULT" | jq -er .session)
result_of() { jq -rs --arg id "$1" '[.[]|select(.type=="user")|.message.content[]?|select(.type=="tool_result" and .tool_use_id==$id)][0]|[.is_error, (.content|if type=="array" then map(.text)|join("") else . end)]|@json' "$SESSION"; }

echo "$RESULT" | jq -e '[.events[]|select(.rule=="sloprail/misplaced-declaration" and .kind=="GateChecked" and (.tool_use_id=="w1" or .tool_use_id=="s1"))] | length==0' >/dev/null ||
  fail "the gate saw the declaration written from inside a script: this case no longer tests that blind spot"
echo "$RESULT" | jq -e '.exit==0' >/dev/null || fail "the agent did not finish"
# the Stop never logs an unjudged range as a refusal
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .on=="Stop")] | map(select(.outcome=="refused")) | length==0' >/dev/null ||
  fail "the Stop logged a refusal for a range nobody judged"
# the refusal itself, as the agent reads it from `sr-checks run`, names where the declaration belongs
c1=$(result_of c1)
echo "$c1" | jq -e '.[0]==true and (.[1]|contains(".sloprail/file-guard/structure.yaml"))' >/dev/null || fail "sr-checks run (c1) did not refuse naming .sloprail/file-guard/structure.yaml: $c1"
# the fix is not refused by any gate, and the second judging passes
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and (.tool_use_id=="b2" or .tool_use_id=="b3") and .outcome=="refused")] | length==0' >/dev/null ||
  fail "the fixing commands were refused by a gate"
c2=$(result_of c2)
echo "$c2" | jq -e '.[0]!=true' >/dev/null || fail "sr-checks run (c2) over the moved declaration was refused: $c2"
# the logged refusal carries the reason too
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/misplaced-declaration" and .outcome=="refused")] | length>=1 and (.[0].reason|contains(".sloprail/file-guard/structure.yaml"))' >/dev/null ||
  fail "the logged refusal does not name .sloprail/file-guard/structure.yaml"
# the permit side of the file-guard is in the log: after the move it passed
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/misplaced-declaration" and .on=="sr-checks run")] | length>=2 and .[0].outcome=="refused" and .[-1].outcome=="passed"' >/dev/null ||
  fail "misplaced-declaration did not go from refused to passed in the log"
# the PreFileWrite gate: refuses .sloprail/structure.yaml with where it belongs, permits .sloprail/file-guard/structure.yaml
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/misplaced-declaration" and .tool_use_id=="gw1")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains(".sloprail/file-guard/structure.yaml"))' >/dev/null ||
  fail "gw1: the gate did not refuse .sloprail/structure.yaml naming .sloprail/file-guard/structure.yaml"
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/misplaced-declaration" and .tool_use_id=="gw2")] | length==1 and .[0].outcome=="permitted"' >/dev/null ||
  fail "gw2: the gate refused .sloprail/file-guard/structure.yaml"
test -f .sloprail/file-guard/structure.yaml
test ! -e .sloprail/structure.yaml
