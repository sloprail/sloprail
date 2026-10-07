#!/usr/bin/env bash
set -euo pipefail

# An mcp__browser__fill_form action followed by an mcp__browser__screenshot whose (text) result describes the
# filled form is permitted at Stop: the prepare matched both real MCP tool names and handed the judge the proof.
git init -q .
mkdir -p .sloprail/gate
cp -R "$SR_TEST_SLOPRAIL_DIR/gate/screenshot-proves-fields" .sloprail/gate/
rm -rf .sloprail/gate/screenshot-proves-fields/tests
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m init
export SR_CHECKS_JUDGE_MOCKS='{"gate/screenshot-proves-fields/screenshot-shows-all-fields":"'"$SR_TEST_CASE_DIR"'/judge.sh"}'
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "fill the contact form and prove it with a screenshot")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="screenshot-proves-fields")|{kind,rule,outcome,on,reason}' >&2; exit 1; }

echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="screenshot-proves-fields")] | length>=1 and all(.[]; .outcome=="permitted")' >/dev/null || fail "an action with a screenshot proof was refused, or the gate never ran"
