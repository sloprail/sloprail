#!/usr/bin/env bash
set -euo pipefail
# rule-tests-rigorous, the plugin manifest is part of the verdict. A case asserts the events of "p/demo" and passes;
# renaming the plugin to "q" in plugin.json (no file of the case or the rule changes) makes the owner's events
# "q/demo", so the same case is refused naming q/demo. A verdict stored before the rename must not be replayed:
# the fingerprint of the case includes the manifest.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")
: > "$SR_EVENTS_FILE"
mkdir -p plugins/p/.claude-plugin plugins/p/.sloprail/gate/demo/tests/demo-case
printf '{"name":"p","version":"0.0.1"}\n' > plugins/p/.claude-plugin/plugin.json
cat "$F/demo-gate.yaml" > plugins/p/.sloprail/gate/demo/gate.yaml
printf '#!/usr/bin/env bash\necho "{\\"reason\\":\\"forbidden/ is read-only; write under allowed/ instead\\"}"\nexit 1\n' > plugins/p/.sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a plugin with a demo gate"
BASE=$(git rev-parse HEAD)
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/rule-tests-rigorous/judge":"'"$SR_TEST_CASE_DIR"'/judge-rigor.sh"}'

# the case asserts p/demo: judged and passed
cat "$F/qualified-case.txt" > plugins/p/.sloprail/gate/demo/tests/demo-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a case that asserts p/demo"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==1 and .[0].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null

# the plugin is renamed: nothing else changes, yet the case no longer proves the rule under its new name
printf '{"name":"q","version":"0.0.1"}\n' > plugins/p/.claude-plugin/plugin.json
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "rename the plugin to q"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==2 and .[1].outcome=="refused" and (.[1].reason|contains("asserts no event of its owning rule q/demo"))' "$SR_EVENTS_FILE" >/dev/null

# the recovery the refusal asks for: the case asserts the gate events of q/demo, and the rule passes the net range
cat "$F/renamed-case.txt" > plugins/p/.sloprail/gate/demo/tests/demo-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "assert q/demo"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==3 and .[2].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
