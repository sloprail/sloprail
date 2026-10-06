#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, a case that ERRORS (sr-test could not run it: exit 2), as against one that fails. The base
# holds a demo gate with one passing case; the range edits that case so that it exits 2.
#   1. the range is refused (an error stays fail-closed) and names the case with status "error";
#   2. but no verdict is stored under the key: `sr-checks verify` says "not judged yet" (a refusal for a failing
#      case would be stored and read back), so the next `run` tries again instead of replaying the error;
#   3. the case fixed (a different content) passes.
# The range also edits a second gate, "calm", whose case passes: the error is the demo subject's alone, so the
# reason names gate/demo and not gate/calm.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/file-guard
mkdir -p .sloprail/gate/demo/tests/the-case .sloprail/gate/calm/tests/ok
cat "$F/demo-gate.yaml" > .sloprail/gate/calm/gate.yaml
cat "$F/demo-refuse.txt" > .sloprail/gate/calm/refuse.sh
cat "$F/passing.txt" > .sloprail/gate/calm/tests/ok/test.sh
chmod +x .sloprail/gate/calm/refuse.sh .sloprail/gate/calm/tests/ok/test.sh
cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
cat "$F/demo-refuse.txt" > .sloprail/gate/demo/refuse.sh
cat "$F/passing.txt" > .sloprail/gate/demo/tests/the-case/test.sh
chmod +x .sloprail/gate/demo/tests/the-case/test.sh .sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a project with a case"
BASE=$(git rev-parse HEAD)

cat "$F/erroring.txt" > .sloprail/gate/demo/tests/the-case/test.sh
printf '# a note\n' >> .sloprail/gate/calm/tests/ok/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "the case errors"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("gate/demo:the-case: error")) and (.[0].reason|contains("gate/calm:")|not)' "$SR_EVENTS_FILE" >/dev/null

# no verdict was stored: verify reads "not judged yet" (a failing case's stored refusal would be read back)
sr-checks verify --base "$BASE" --head HEAD >verify.out 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
grep -q "not judged yet" verify.out
rm -f verify.out

# the recovery: the case is fixed and passes (verify above logged event 2, so this run is event 3)
cat "$F/fixed.txt" > .sloprail/gate/demo/tests/the-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "restore the case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==3 and .[2].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
