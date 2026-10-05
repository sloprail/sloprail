#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, the scope. The demo gate has two cases and the base is committed with the second ("broken")
# failing: nobody has touched it since.
#   1. a range that edits only the case "good" is a tests-only change: only gate/demo:good runs, so the broken
#      sibling it did not touch does not refuse the range;
#   2. the same range also edits the gate's script: a rule changed, so ALL the cases run and the broken sibling
#      is refused, named, with its output (and the good one is not named);
#   3. fixing the broken case passes the range.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/file-guard
mkdir -p .sloprail/gate/demo/tests/good .sloprail/gate/demo/tests/broken
cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
cat "$F/demo-refuse.txt" > .sloprail/gate/demo/refuse.sh
cat "$F/passing.txt" > .sloprail/gate/demo/tests/good/test.sh
cat "$F/failing.txt" > .sloprail/gate/demo/tests/broken/test.sh
chmod +x .sloprail/gate/demo/refuse.sh .sloprail/gate/demo/tests/good/test.sh .sloprail/gate/demo/tests/broken/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a gate with a good case and a broken one"
BASE=$(git rev-parse HEAD)

# 1. tests only: the case "good" is edited, "broken" is not touched
printf '# a note\n' >> .sloprail/gate/demo/tests/good/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "edit the good case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==1 and .[0].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null

# 2. the rule changes too: every case runs, the untouched broken one included
printf '# a note\n' >> .sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "edit the gate"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==2 and .[1].outcome=="refused" and (.[1].reason|contains("gate/demo:broken: fail")) and (.[1].reason|contains("the demo asserts 1 == 2")) and (.[1].reason|contains("all the cases")) and (.[1].reason|contains("gate/demo:good")|not)' "$SR_EVENTS_FILE" >/dev/null

# 3. the recovery: the broken case is fixed
cat "$F/fixed.txt" > .sloprail/gate/demo/tests/broken/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "fix the broken case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==3 and .[2].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
