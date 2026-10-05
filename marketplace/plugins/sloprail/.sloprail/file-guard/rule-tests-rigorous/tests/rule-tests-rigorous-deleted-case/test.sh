#!/usr/bin/env bash
set -euo pipefail
# rule-tests-rigorous, a deleted case. The base holds a demo gate and a sloppy case for it (legacy: it is in
# the base, nobody judges it). A commit that edits the case judges it and is refused (no shebang, no owner
# event). A commit that deletes it and adds a rigorous case beside it judges only the new case: the deleted
# one is no subject, so it raises no refusal and the range passes.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/gate/demo/tests/demo-case
cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
printf '#!/usr/bin/env bash\necho "{\\"reason\\":\\"forbidden/ is read-only; write under allowed/ instead\\"}"\nexit 1\n' > .sloprail/gate/demo/refuse.sh
cat "$F/sloppy-case.txt" > .sloprail/gate/demo/tests/demo-case/test.sh
mkdir -p .sloprail/file-guard
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a demo gate with a sloppy case"
BASE=$(git rev-parse HEAD)
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/rule-tests-rigorous/judge":"'"$SR_TEST_CASE_DIR"'/judge-rigor.sh"}'

# edited: the case is a subject again and is refused
printf '# touched\n' >> .sloprail/gate/demo/tests/demo-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "touch the sloppy case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("gate/demo/tests/demo-case")) and (.[0].reason|contains("does not start with a shebang"))' "$SR_EVENTS_FILE" >/dev/null

# deleted, with a rigorous case beside it: only the new case is a subject, the deleted one is not refused
git rm -q -r .sloprail/gate/demo/tests/demo-case
mkdir -p .sloprail/gate/demo/tests/good-case
cat "$F/good-case.txt" > .sloprail/gate/demo/tests/good-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "delete the case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==2 and .[1].outcome=="passed" and (.[1].reason // "" | contains("demo-case") | not)' "$SR_EVENTS_FILE" >/dev/null
