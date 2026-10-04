#!/usr/bin/env bash
set -euo pipefail

# A declaration written one level too high from inside a script is invisible to the PreFileWrite gate.
# Stop refuses ("not judged yet"); `sr-checks run` as Stop names it refuses with where the file belongs;
# the agent moves it and commits (move and commit as separate commands, as cite-before-commit demands);
# Stop is told to judge the new range, which passes.
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "write the structure one level too high")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|{kind,rule,outcome,on,tool_use_id,reason}' >&2; exit 1; }
SESSION=$(echo "$RESULT" | jq -er .session)
result_of() { jq -rs --arg id "$1" '[.[]|select(.type=="user")|.message.content[]?|select(.type=="tool_result" and .tool_use_id==$id)][0]|[.is_error, (.content|if type=="array" then map(.text)|join("") else . end)]|@json' "$SESSION"; }

echo "$RESULT" | jq -e '[.events[]|select(.rule=="sloprail/misplaced-declaration" and .kind=="GateChecked")] | length==0' >/dev/null ||
  fail "the gate saw the script write: this case no longer tests the Stop path"
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/misplaced-declaration" and .on=="Stop")] | length>=2 and .[0].outcome=="refused" and (.[0].reason|contains("not judged yet")) and .[-1].outcome=="passed"' >/dev/null ||
  fail "misplaced-declaration did not refuse at Stop and then pass after the move"
# the refusal itself, as the agent reads it from `sr-checks run`, names where the declaration belongs
c1=$(result_of c1)
echo "$c1" | jq -e '.[0]==true and (.[1]|contains(".sloprail/file-guard/structure.yaml"))' >/dev/null || fail "sr-checks run (c1) did not refuse naming .sloprail/file-guard/structure.yaml: $c1"
# the fix is not refused by any gate, and the second judging passes
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and (.tool_use_id=="b2" or .tool_use_id=="b3") and .outcome=="refused")] | length==0' >/dev/null ||
  fail "the fixing commands were refused by a gate"
c2=$(result_of c2)
echo "$c2" | jq -e '.[0]!=true' >/dev/null || fail "sr-checks run (c2) over the moved declaration was refused: $c2"
test -f .sloprail/file-guard/structure.yaml
test ! -e .sloprail/structure.yaml
