#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, a rule nobody proves fires. The base holds a legacy gate with no case (it must NOT be
# refused for that) and a gate with a case. The range adds a new gate "demo" with no case of its own: the
# refusal names gate/demo (and not the legacy gate), saying where a case goes. The agent adds a case in the
# demo gate's folder whose agent makes the gate refuse and then permit, and the net range passes.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/file-guard
mkdir -p .sloprail/gate/legacy .sloprail/gate/other/tests/ok .sloprail/gate/demo
cat "$F/legacy-gate.yaml" > .sloprail/gate/legacy/gate.yaml
cat "$F/legacy-check.txt" > .sloprail/gate/legacy/refuse.sh
cat "$F/legacy-gate.yaml" > .sloprail/gate/other/gate.yaml
cat "$F/legacy-check.txt" > .sloprail/gate/other/refuse.sh
cat "$F/passing.txt" > .sloprail/gate/other/tests/ok/test.sh
chmod +x .sloprail/gate/other/tests/ok/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a project with a legacy gate and a covered gate"
BASE=$(git rev-parse HEAD)

cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
cat "$F/demo-refuse.txt" > .sloprail/gate/demo/refuse.sh
chmod +x .sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a new gate with no case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("gate/demo")) and (.[0].reason|contains("no sr-test case lives in the folder")) and (.[0].reason|contains(".sloprail/gate/demo/tests/<case>/")) and (.[0].reason|contains("legacy")|not)' "$SR_EVENTS_FILE" >/dev/null

# the recovery: a case in the gate's folder that makes it refuse and permit
mkdir -p .sloprail/gate/demo/tests/demo-case
cat "$F/demo-case-test.txt" > .sloprail/gate/demo/tests/demo-case/test.sh
cat "$F/demo-case-agent.txt" > .sloprail/gate/demo/tests/demo-case/agent.sh
chmod +x .sloprail/gate/demo/tests/demo-case/test.sh .sloprail/gate/demo/tests/demo-case/agent.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a case for the demo gate"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==2 and .[1].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
