#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, a plugin whose plugin.json has NO `name`. sr-test names such a plugin by its folder, so
# the coverage check must not rebuild the name itself from `.name`: a changed rule of the plugin with no case
# is still refused (a name it guessed differently would have skipped the refusal silently).
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")
: > "$SR_EVENTS_FILE"
mkdir -p plugins/unnamed/.claude-plugin plugins/unnamed/.sloprail/file-guard
printf '{}\n' > plugins/unnamed/.claude-plugin/plugin.json
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a plugin with no name"
BASE=$(git rev-parse HEAD)

# the range adds a gate to the plugin's .sloprail with no case
mkdir -p plugins/unnamed/.sloprail/gate/demo
cat "$F/demo-gate.yaml" > plugins/unnamed/.sloprail/gate/demo/gate.yaml
cat "$F/demo-refuse.txt" > plugins/unnamed/.sloprail/gate/demo/refuse.sh
chmod +x plugins/unnamed/.sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a gate with no case in the unnamed plugin"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("gate/demo")) and (.[0].reason|contains("no sr-test case lives in the folder")) and (.[0].reason|contains("plugins/unnamed/.sloprail/gate/demo/tests/<case>/"))' "$SR_EVENTS_FILE" >/dev/null

# the recovery: a case in the plugin gate's folder that makes it refuse and permit; the range now passes
mkdir -p plugins/unnamed/.sloprail/gate/demo/tests/demo-case
cat "$F/unnamed-case-test.txt" > plugins/unnamed/.sloprail/gate/demo/tests/demo-case/test.sh
cat "$F/demo-case-agent.txt" > plugins/unnamed/.sloprail/gate/demo/tests/demo-case/agent.sh
chmod +x plugins/unnamed/.sloprail/gate/demo/tests/demo-case/test.sh plugins/unnamed/.sloprail/gate/demo/tests/demo-case/agent.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a case for the demo gate of the unnamed plugin"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==2 and .[1].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
