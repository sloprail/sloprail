#!/usr/bin/env bash
set -euo pipefail
# plugin-skill-length refuses a write that leaves a skill file of the sloprail plugin at 201 lines,
# permits the same file at 200, and does not judge a long file elsewhere.
git init -q .
mkdir -p .sloprail/gate .sloprail/file-guard/plugin-skill-length
cp -R "$SR_TEST_SLOPRAIL_DIR/gate/plugin-skill-length" .sloprail/gate/
rm -rf .sloprail/gate/plugin-skill-length/tests
cp "$SR_TEST_SLOPRAIL_DIR/file-guard/plugin-skill-length/line-cap-lib.sh" .sloprail/file-guard/plugin-skill-length/
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "write a skill")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="plugin-skill-length")|{kind,outcome,tool_use_id,reason}' >&2; exit 1; }
verdict() { echo "$RESULT" | jq -e --arg id "$1" --arg out "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="plugin-skill-length" and .tool_use_id==$id)] | length==1 and .[0].outcome==$out' >/dev/null; }
verdict w1 refused || fail "the 201-line skill file was not refused"
echo "$RESULT" | jq -e '[.events[]|select(.rule=="plugin-skill-length" and .tool_use_id=="w1")][0].reason | contains("201 lines, over the cap of 200")' >/dev/null || fail "the refusal does not name the count and the cap"
verdict w2 permitted || fail "the 200-line skill file was not permitted"
echo "$RESULT" | jq -e '[.events[]|select(.rule=="plugin-skill-length" and .tool_use_id=="x1")] | length==0' >/dev/null || fail "a file outside the plugin's skills was judged"
[ "$(wc -l < marketplace/plugins/sloprail/skills/demo/SKILL.md | tr -d ' ')" -le 200 ] || fail "the 201-line file landed"
