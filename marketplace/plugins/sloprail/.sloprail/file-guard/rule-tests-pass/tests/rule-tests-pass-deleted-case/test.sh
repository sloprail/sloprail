#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, deleted cases. The demo gate has two cases and the base is committed with the second ("b")
# failing, untouched since.
#   1. the range deletes the broken case "b": a deleted case is not run (it would fail) and the remaining
#      "a" passes, so the range passes;
#   2. the range also deletes the last case "a": the demo gate, whose folder the range touched, has no case
#      left, so the range is refused naming gate/demo;
#   3. a new case in its folder passes the range.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/file-guard
mkdir -p .sloprail/gate/demo/tests/a .sloprail/gate/demo/tests/b
cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
cat "$F/demo-refuse.txt" > .sloprail/gate/demo/refuse.sh
cat "$F/passing.txt" > .sloprail/gate/demo/tests/a/test.sh
cat "$F/failing.txt" > .sloprail/gate/demo/tests/b/test.sh
chmod +x .sloprail/gate/demo/refuse.sh .sloprail/gate/demo/tests/a/test.sh .sloprail/gate/demo/tests/b/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a gate with two cases, one broken"
BASE=$(git rev-parse HEAD)

git rm -q -r .sloprail/gate/demo/tests/b
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "delete the broken case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==1 and .[0].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null

git rm -q -r .sloprail/gate/demo/tests/a
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "delete the last case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==2 and .[1].outcome=="refused" and (.[1].reason|contains("gate/demo")) and (.[1].reason|contains("no sr-test case lives in the folder"))' "$SR_EVENTS_FILE" >/dev/null

mkdir -p .sloprail/gate/demo/tests/c
cat "$F/passing.txt" > .sloprail/gate/demo/tests/c/test.sh
chmod +x .sloprail/gate/demo/tests/c/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a new case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==3 and .[2].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
