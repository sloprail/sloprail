#!/usr/bin/env bash
set -euo pipefail

# The PreFileWrite half of authoring-slop. A hook script that reads .event.newContent without .resultKnown is
# refused before it lands, naming the rule and the fix; the same script that asks resultKnown first is permitted.
# A script written by a shell redirect has content the engine cannot compute: refused, told to write it
# directly; written with the Write tool it is permitted.
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "add a gate with a script")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/authoring-slop")|{kind,rule,outcome,on,tool_use_id,reason}' >&2; exit 1; }

ev() { echo "$RESULT" | jq -e --arg id "$1" --arg out "$2" --arg s "${3:-}" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/authoring-slop" and .tool_use_id==$id)] | length==1 and .[0].outcome==$out and ((.[0].reason // "")|contains($s))' >/dev/null; }
ev w1 refused "content-may-be-unresolvable" || fail "w1: the script reading newContent without resultKnown was not refused with the rule"
echo "$RESULT" | jq -e '[.events[]|select(.rule=="sloprail/authoring-slop" and .tool_use_id=="w1")][0].reason|contains("resultKnown")' >/dev/null || fail "w1: the refusal does not name resultKnown"
ev w2 permitted || fail "w2: the script that checks resultKnown was refused"
ev b1 refused "Write the file's content directly" || fail "b1: a redirect-written script was not refused with 'Write the file's content directly'"
ev w3 permitted || fail "w3: the directly written script was refused"
# only the permitted scripts exist
grep -q 'resultKnown' .sloprail/gate/demo/check.sh || fail "check.sh on disk is not the fixed one"
test "$(cat .sloprail/gate/demo/other.sh)" = $'#!/usr/bin/env bash\nexit 0' || fail "other.sh on disk is not the directly written one"
