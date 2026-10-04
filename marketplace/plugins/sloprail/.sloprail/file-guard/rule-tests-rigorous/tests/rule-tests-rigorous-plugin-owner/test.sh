#!/usr/bin/env bash
set -euo pipefail
# rule-tests-rigorous, the owner of a plugin's rule. The case sits in plugins/p/.sloprail/gate/demo/tests/demo-case/,
# and plugins/p/.claude-plugin/plugin.json names the plugin "p": the owner's events carry the rule "p/demo",
# not the bare "demo". A case asserting the bare name is refused naming "p/demo"; asserting "p/demo" passes.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
: > "$SR_EVENTS_FILE"
mkdir -p plugins/p/.claude-plugin plugins/p/.sloprail/gate/demo/tests/demo-case
printf '{"name":"p","version":"0.0.1"}\n' > plugins/p/.claude-plugin/plugin.json
cat "$F/demo-gate.yaml" > plugins/p/.sloprail/gate/demo/gate.yaml
printf '#!/usr/bin/env bash\necho "{\\"reason\\":\\"forbidden/ is read-only; write under allowed/ instead\\"}"\nexit 1\n' > plugins/p/.sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a plugin with a demo gate"
BASE=$(git rev-parse HEAD)
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/rule-tests-rigorous/judge":"'"$SR_TEST_CASE_DIR"'/judge-rigor.sh"}'

# the bare name: a plugin's rule is named <plugin>/<rule>
cat "$F/bare-name-case.txt" > plugins/p/.sloprail/gate/demo/tests/demo-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a case that asserts the bare rule name"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("asserts no event of its owning rule p/demo")) and (.[0].reason|contains("plugins/p/.sloprail/gate/demo/tests/demo-case"))' "$SR_EVENTS_FILE" >/dev/null

# the recovery: the plugin-qualified name
cat "$F/qualified-case.txt" > plugins/p/.sloprail/gate/demo/tests/demo-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "assert p/demo"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==2 and .[1].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
