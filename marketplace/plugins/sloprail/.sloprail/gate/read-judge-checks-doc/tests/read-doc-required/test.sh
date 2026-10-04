#!/usr/bin/env bash
set -euo pipefail

# read-judge-checks-doc refuses the write of a judge prompt until judge-checks.md was read, naming judge-checks.md (at the root and in a nested
# .sloprail/), though the skill is loaded; reading it makes the same writes permitted, and a near-boundary
# neighbour (.sloprail/gate/demo/judge.md) raises no verdict of this gate at all.
git init -q .
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "write the judge prompt")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/read-judge-checks-doc")|{kind,rule,outcome,tool_use_id,reason}' >&2; exit 1; }
N=marketplace/plugins/x
verdict() { echo "$RESULT" | jq -e --arg id "$1" --arg out "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/read-judge-checks-doc" and .tool_use_id==$id)] | length==1 and .[0].outcome==$out' >/dev/null; }

for id in ra na; do
  verdict $id refused || fail "$id: the write before reading the page was not refused"
  echo "$RESULT" | jq -e --arg id $id '[.events[]|select(.rule=="sloprail/read-judge-checks-doc" and .tool_use_id==$id)][0].reason | contains("READ REQUIRED") and contains("judge-checks.md")' >/dev/null || fail "$id: the refusal does not name judge-checks.md"
done
# the refused writes never ran: their tool_results are errors
SESSION=$(echo "$RESULT" | jq -er .session)
for id in ra na; do
  jq -es --arg id $id 'any(.[]; .type=="user" and any(.message.content[]?; .type=="tool_result" and .tool_use_id==$id and .is_error==true))' "$SESSION" >/dev/null || fail "$id: the refused write has no error tool_result"
done
for id in rb nb; do verdict $id permitted || fail "$id: the write after reading the page was not permitted"; done
test -f .sloprail/gate/demo/judge.md.j2 || fail ".sloprail/gate/demo/judge.md.j2 was not written after the page was read"
test -f $N/.sloprail/gate/demo/judge.md.j2 || fail "the nested declaration was not written after the page was read"
# near boundary: the neighbour is not this gate's business, and landed
echo "$RESULT" | jq -e '[.events[]|select(.rule=="sloprail/read-judge-checks-doc" and .tool_use_id=="xb")] | length==0' >/dev/null || fail "xb: the neighbour .sloprail/gate/demo/judge.md was judged by read-judge-checks-doc"
test -f .sloprail/gate/demo/judge.md || fail "the neighbour .sloprail/gate/demo/judge.md was not written"
