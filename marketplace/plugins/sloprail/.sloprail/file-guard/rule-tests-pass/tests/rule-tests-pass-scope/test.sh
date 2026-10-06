#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, one subject per rule. Two gates, "alpha" and "beta", each with one case; beta's case is
# broken and committed so in the base, untouched since.
#   1. a range that touches only alpha (its case and its script) judges only alpha's subject: beta's broken
#      case is not run, so the range passes;
#   2. a range that also touches beta judges beta's subject too: it is refused, naming gate/beta:broken with
#      its output, and never naming alpha (alpha's own subject passed);
#   3. fixing beta's case passes the range.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/file-guard
mkdir -p .sloprail/gate/alpha/tests/good .sloprail/gate/beta/tests/broken
for r in alpha beta; do
  cat "$F/demo-gate.yaml" > .sloprail/gate/$r/gate.yaml
  cat "$F/demo-refuse.txt" > .sloprail/gate/$r/refuse.sh
  chmod +x .sloprail/gate/$r/refuse.sh
done
cat "$F/passing.txt" > .sloprail/gate/alpha/tests/good/test.sh
cat "$F/failing.txt" > .sloprail/gate/beta/tests/broken/test.sh
chmod +x .sloprail/gate/alpha/tests/good/test.sh .sloprail/gate/beta/tests/broken/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "two gates, beta's case broken"
BASE=$(git rev-parse HEAD)

# 1. only alpha is touched: beta's broken case is not judged
printf '# a note\n' >> .sloprail/gate/alpha/tests/good/test.sh
printf '# a note\n' >> .sloprail/gate/alpha/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "edit alpha"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==1 and .[0].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null

# 2. beta is touched too: its subject is judged and refused, alpha's is not named
printf '# a note\n' >> .sloprail/gate/beta/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "edit beta"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==2 and .[1].outcome=="refused" and (.[1].reason|contains("gate/beta:broken: fail")) and (.[1].reason|contains("the demo asserts 1 == 2")) and (.[1].reason|contains("--rule gate/beta")) and (.[1].reason|contains("gate/alpha:")|not) and (.[1].error != true)' "$SR_EVENTS_FILE" >/dev/null

# 3. the recovery: beta's case is fixed
cat "$F/fixed.txt" > .sloprail/gate/beta/tests/broken/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "fix beta's case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==3 and .[2].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
