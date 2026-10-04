#!/usr/bin/env bash
set -euo pipefail
# rule-tests-rigorous, the owner. A case's rule is the folder it sits in: .sloprail/gate/demo/tests/demo-case/
# proves the gate "demo" and nothing else. Two cases are refused by the script floor, naming the owner and its
# event kind: one that asserts only the events of ANOTHER rule ("other"), one that asserts "demo" but through
# the wrong kind (FileGuardChecked, though demo is a gate). The third asserts GateChecked events of "demo" and
# passes (its judge is mocked, and refuses a prompt that lacks the owning rule's files).
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/gate/demo/tests/demo-case .sloprail/gate/other
cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
cat "$F/demo-gate.yaml" > .sloprail/gate/other/gate.yaml
printf '#!/usr/bin/env bash\necho "{\\"reason\\":\\"forbidden/ is read-only; write under allowed/ instead\\"}"\nexit 1\n' > .sloprail/gate/demo/refuse.sh
cp .sloprail/gate/demo/refuse.sh .sloprail/gate/other/refuse.sh
mkdir -p .sloprail/file-guard
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a demo gate and another gate"
BASE=$(git rev-parse HEAD)
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/rule-tests-rigorous/judge":"'"$SR_TEST_CASE_DIR"'/judge-rigor.sh"}'

# a case that asserts only the events of another rule
cat "$F/other-rule-case.txt" > .sloprail/gate/demo/tests/demo-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a case that asserts another rule"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("asserts no event of its owning rule demo")) and (.[0].reason|contains("GateChecked"))' "$SR_EVENTS_FILE" >/dev/null

# the owner, through the wrong event kind: still refused, for the same reason
cat "$F/wrong-kind-case.txt" > .sloprail/gate/demo/tests/demo-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a case that asserts demo through FileGuardChecked"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==2 and .[1].outcome=="refused" and (.[1].reason|contains("asserts no event of its owning rule demo")) and (.[1].reason|contains("GateChecked"))' "$SR_EVENTS_FILE" >/dev/null

# the recovery the refusal asked for: the owner's GateChecked events, refusal and permit
cat "$F/owner-case.txt" > .sloprail/gate/demo/tests/demo-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "assert the owner"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==3 and .[2].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
