#!/usr/bin/env bash
set -euo pipefail

# cite-before-commit stops the commit that changes a rule which already stands without a citation riding on it,
# naming the trailer to add; a commit of a new file (which weakens nothing) is untouched. With the trailer the
# commit goes through, as does a follow-up commit that really changes the file and cites the words that ask for it.
# (What `sr-checks run` then makes of the cited quotes is grounded-rule-changes' business, tested there.)
git init -q .
mkdir -p .sloprail/gate/demo
printf 'on:\n  - event: Stop\n  - event: PreFileWrite\n    match: "true"\n' > .sloprail/gate/demo/gate.yaml
git add .sloprail/gate/demo/gate.yaml
git -c user.name=t -c user.email=t@t commit -q -m "a gate that already stands"
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "tighten the demo gate to run only on Stop. Also tell me the time.")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/cite-before-commit")|{kind,rule,outcome,on,tool_use_id,reason}' >&2; exit 1; }
cite() { echo "$RESULT" | jq -e --arg id "$1" --arg out "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/cite-before-commit" and .tool_use_id==$id)] | length==1 and .[0].outcome==$out' >/dev/null; }

cite b0 permitted || fail "b0: committing a new file was held up for a citation"
cite b1 refused   || fail "b1: changing a standing rule with no citation was not refused"
echo "$RESULT" | jq -e '[.events[]|select(.rule=="sloprail/cite-before-commit" and .tool_use_id=="b1")][0].reason | contains("Sloprail-Cites-User") and contains(".sloprail/gate/demo/gate.yaml")' >/dev/null || fail "b1: the refusal does not name the trailer and the file"
cite b2 permitted || fail "b2: a commit carrying a resolving quote was refused"
cite b3 permitted || fail "b3: the follow-up commit with the trailer was refused"
# and the uncited commit never happened
test "$(git log --format=%s | grep -c 'tighten the demo gate$')" = 1 || fail "the refused commit b1 landed"
