#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, a file MODE is not a rule change. The base holds a legacy gate "demo" (no case, its script
# committed without the executable bit).
#   1. a range whose only change is `chmod +x` on that script (identical bytes) passes: the rule is not
#      "added or edited", so it is not refused for having no case (permit, nearest the boundary);
#   2. the same script with one byte of content changed (the mode too) is a rule change: refused, naming
#      gate/demo and the folder a case goes in;
#   3. the recovery: a case in the rule's folder, and the range passes.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/file-guard .sloprail/gate/demo
cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
cat "$F/demo-refuse.txt" > .sloprail/gate/demo/refuse.sh
chmod 644 .sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a legacy gate with no case"
BASE=$(git rev-parse HEAD)

# 1. mode only
chmod +x .sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "make the script executable"
git diff --summary "$BASE" HEAD | grep -q 'mode change 100644 => 100755'
[ -z "$(git diff "$BASE" HEAD --numstat | awk '$1 != 0 || $2 != 0')" ]
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==1 and .[0].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null

# 2. one byte of content: a rule change with no case
printf '# a note\n' >> .sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "edit the script"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==2 and .[1].outcome=="refused" and (.[1].reason|contains("gate/demo")) and (.[1].reason|contains("no sr-test case lives in the folder")) and (.[1].reason|contains(".sloprail/gate/demo/tests/<case>/"))' "$SR_EVENTS_FILE" >/dev/null

# 3. the recovery: a case in the rule's folder
mkdir -p .sloprail/gate/demo/tests/demo-case
cat "$F/demo-case-test.txt" > .sloprail/gate/demo/tests/demo-case/test.sh
cat "$F/demo-case-agent.txt" > .sloprail/gate/demo/tests/demo-case/agent.sh
chmod +x .sloprail/gate/demo/tests/demo-case/test.sh .sloprail/gate/demo/tests/demo-case/agent.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a case for the demo gate"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==3 and .[2].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
