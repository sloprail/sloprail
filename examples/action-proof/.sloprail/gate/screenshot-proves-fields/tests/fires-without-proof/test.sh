#!/usr/bin/env bash
set -euo pipefail

# An mcp__browser__fill_form action with no screenshot proof is refused at Stop, with the judge's
# reasoning: the gate's prepare recognised the real MCP tool name, found no proof, and the judge failed it.
git init -q .
mkdir -p .sloprail/gate
cp -R "$SR_TEST_SLOPRAIL_DIR/gate/screenshot-proves-fields" .sloprail/gate/
rm -rf .sloprail/gate/screenshot-proves-fields/tests
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m init
export SR_CHECKS_JUDGE_MOCKS='{"gate/screenshot-proves-fields/screenshot-shows-all-fields":"'"$SR_TEST_CASE_DIR"'/judge.sh"}'
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "fill the contact form")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="screenshot-proves-fields")|{kind,rule,outcome,on,reason}' >&2; exit 1; }

echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="screenshot-proves-fields")] | length>=1 and .[0].outcome=="refused" and (.[0].reason|contains("the action has no screenshot proof"))' >/dev/null || fail "the fill_form action with no screenshot was not refused with the judge's reasoning"
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="screenshot-proves-fields" and .outcome=="permitted")] | length==0' >/dev/null || fail "the gate permitted a turn whose fill_form action has no proof"
