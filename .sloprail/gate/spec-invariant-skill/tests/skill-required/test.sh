#!/usr/bin/env bash
set -euo pipefail
# spec-invariant-skill refuses a write under spec/<domain>/invariants/ until document-invariant was loaded, leaves a
# neighbour outside that folder alone, and permits the same write once the skill was loaded.
git init -q .
mkdir -p .sloprail/gate .claude/skills
cp -R "$SR_TEST_SLOPRAIL_DIR/gate/spec-invariant-skill" .sloprail/gate/
rm -rf .sloprail/gate/spec-invariant-skill/tests
cp -R "$SR_TEST_SLOPRAIL_DIR/../.claude/skills/document-invariant" .claude/skills/
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "write a spec invariant")
R=spec-invariant-skill
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="spec-invariant-skill")|{kind,outcome,tool_use_id,reason}' >&2; exit 1; }
verdict() { echo "$RESULT" | jq -e --arg id "$1" --arg out "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="spec-invariant-skill" and .tool_use_id==$id)] | length==1 and .[0].outcome==$out' >/dev/null; }
verdict wa refused || fail "the write before loading document-invariant was not refused"
echo "$RESULT" | jq -e '[.events[]|select(.rule=="spec-invariant-skill" and .tool_use_id=="wa")][0].reason | contains("document-invariant")' >/dev/null || fail "the refusal does not name document-invariant"
echo "$RESULT" | jq -e '[.events[]|select(.rule=="spec-invariant-skill" and .tool_use_id=="xb")] | length==0' >/dev/null || fail "the neighbour spec/demo/README.md was judged"
verdict wb permitted || fail "the write after loading document-invariant was not permitted"
test -f spec/demo/invariants/a.yaml || fail "spec/demo/invariants/a.yaml was not written after the skill was loaded"
