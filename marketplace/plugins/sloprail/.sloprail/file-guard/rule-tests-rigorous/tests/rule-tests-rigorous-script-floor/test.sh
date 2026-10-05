#!/usr/bin/env bash
set -euo pipefail
# rule-tests-rigorous, the script floor, on the CI path (no agent): sr-checks run judges a committed range.
# The base holds a demo gate; the range adds a sloppy case for it (no shebang, only an exit code asserted,
# a failure swallowed, no event of its owning rule). The refusal names each shape; a second commit rewrites the same case with the three
# faults fixed, and the rule passes the net range.
# The case sits in its owning rule's folder, .sloprail/gate/demo/tests/demo-case/: the folder names the rule.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/gate/demo .sloprail/gate/demo/tests/demo-case
cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
printf '#!/usr/bin/env bash\necho "{\\"reason\\":\\"forbidden/ is read-only; write under allowed/ instead\\"}"\nexit 1\n' > .sloprail/gate/demo/refuse.sh
mkdir -p .sloprail/file-guard
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a demo gate"
BASE=$(git rev-parse HEAD)
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/rule-tests-rigorous/judge":"'"$SR_TEST_CASE_DIR"'/judge-rigor.sh"}'

cat "$F/sloppy-case.txt" > .sloprail/gate/demo/tests/demo-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a sloppy case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("does not start with a shebang")) and (.[0].reason|contains("asserts no event of its owning rule demo")) and (.[0].reason|contains("never asserts on the events")) and (.[0].reason|contains("|| true"))' "$SR_EVENTS_FILE" >/dev/null

# the recovery the refusal asked for
cat "$F/fixed-case.txt" > .sloprail/gate/demo/tests/demo-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "fix the case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==2 and .[1].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
