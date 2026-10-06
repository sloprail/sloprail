#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, a file no rule owns. Two gates: "alpha" has a broken case (committed so in the base, untouched
# since) and sources a helper that "beta" keeps (../beta/helper.sh); "beta" has a passing case. A shared
# .sloprail/lib/common.sh sits beside them.
#   1. a range that edits only beta's helper (a file of ANOTHER rule that alpha names) judges alpha too: refused,
#      naming gate/alpha:broken, and not gate/beta (beta's own case passes);
#   2. a range that edits only the shared lib/common.sh makes every rule of the root a subject: refused the same
#      way, naming gate/alpha:broken and not gate/beta;
#   3. alpha's case fixed: the same shared edit passes.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/file-guard .sloprail/lib
mkdir -p .sloprail/gate/alpha/tests/broken .sloprail/gate/beta/tests/good
for r in alpha beta; do
  cat "$F/demo-gate.yaml" > .sloprail/gate/$r/gate.yaml
  cat "$F/demo-refuse.txt" > .sloprail/gate/$r/refuse.sh
  chmod +x .sloprail/gate/$r/refuse.sh
done
printf '# sources ../beta/helper.sh\n' >> .sloprail/gate/alpha/refuse.sh
printf '# a helper alpha sources\n' > .sloprail/gate/beta/helper.sh
printf '# shared by the rules\n' > .sloprail/lib/common.sh
cat "$F/failing.txt" > .sloprail/gate/alpha/tests/broken/test.sh
cat "$F/passing.txt" > .sloprail/gate/beta/tests/good/test.sh
chmod +x .sloprail/gate/alpha/tests/broken/test.sh .sloprail/gate/beta/tests/good/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "alpha broken, beta fine, a helper and a shared lib"
BASE=$(git rev-parse HEAD)

# 1. beta's helper, which alpha sources: alpha's broken case is judged
printf '# edited\n' >> .sloprail/gate/beta/helper.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "edit beta's helper"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("gate/alpha:broken: fail")) and (.[0].reason|contains("gate/beta:")|not)' "$SR_EVENTS_FILE" >/dev/null

# 2. a new base, still with alpha broken: only the shared lib changes
BASE=$(git rev-parse HEAD)
printf '# edited\n' >> .sloprail/lib/common.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "edit the shared lib"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==2 and .[1].outcome=="refused" and (.[1].reason|contains("gate/alpha:broken: fail")) and (.[1].reason|contains("the demo asserts 1 == 2")) and (.[1].reason|contains("gate/beta:")|not)' "$SR_EVENTS_FILE" >/dev/null

# 3. the recovery: alpha's case is fixed, the shared edit passes
BASE=$(git rev-parse HEAD)
cat "$F/passing.txt" > .sloprail/gate/alpha/tests/broken/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "fix alpha's case"
BASE=$(git rev-parse HEAD)
printf '# edited again\n' >> .sloprail/lib/common.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "edit the shared lib again"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==3 and .[2].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
