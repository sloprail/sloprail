#!/usr/bin/env bash
set -euo pipefail
# spec-entity-skill refuses a write under spec/<domain>/entities/ until document-entity was loaded, leaves a
# neighbour outside that folder alone, and permits the same write once the skill was loaded.
git init -q .
mkdir -p .sloprail/gate .claude/skills
cp -R "$SR_TEST_SLOPRAIL_DIR/gate/spec-entity-skill" .sloprail/gate/
rm -rf .sloprail/gate/spec-entity-skill/tests
cp -R "$SR_TEST_SLOPRAIL_DIR/../.claude/skills/document-entity" .claude/skills/
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "write a spec entity")
R=spec-entity-skill
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="spec-entity-skill")|{kind,outcome,tool_use_id,reason}' >&2; exit 1; }
verdict() { echo "$RESULT" | jq -e --arg id "$1" --arg out "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="spec-entity-skill" and .tool_use_id==$id)] | length==1 and .[0].outcome==$out' >/dev/null; }
verdict wa refused || fail "the write before loading document-entity was not refused"
echo "$RESULT" | jq -e '[.events[]|select(.rule=="spec-entity-skill" and .tool_use_id=="wa")][0].reason | contains("document-entity")' >/dev/null || fail "the refusal does not name document-entity"
echo "$RESULT" | jq -e '[.events[]|select(.rule=="spec-entity-skill" and .tool_use_id=="xb")] | length==0' >/dev/null || fail "the neighbour spec/demo/README.md was judged"
verdict wb permitted || fail "the write after loading document-entity was not permitted"
test -f spec/demo/entities/A.yaml || fail "spec/demo/entities/A.yaml was not written after the skill was loaded"
