#!/usr/bin/env bash
set -euo pipefail
# rule-tests-rigorous, every gap in one verdict. The demo gate's only case passes the script floor (it asserts an
# event of the owner) but asserts neither a refusal nor a permit. The judge (mocked, deciding from the case it
# reads) reports BOTH gaps in its one verdict, one gap per line; the case that asserts the
# refusal and the permit then passes.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")
: > "$SR_EVENTS_FILE"
: > "$SR_EVENTS_FILE.judges"
mkdir -p .sloprail/gate/demo/tests/inert .sloprail/file-guard
cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
printf '#!/usr/bin/env bash\necho "{\\"reason\\":\\"forbidden/ is read-only; write under allowed/ instead\\"}"\nexit 1\n' > .sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a demo gate"
BASE=$(git rev-parse HEAD)
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/rule-tests-rigorous/judge":"'"$SR_TEST_CASE_DIR"'/judge-rigor.sh"}'

cat "$F/inert-case.txt" > .sloprail/gate/demo/tests/inert/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a case that asserts neither a refusal nor a permit"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("no case asserts a refusal")) and (.[0].reason|split("\n")|map(select(startswith("demo: REFUSAL AND PERMIT: no case asserts a")))|length==2)' "$SR_EVENTS_FILE" >/dev/null
[ "$(wc -l < "$SR_EVENTS_FILE.judges" | tr -d ' ')" = 1 ]

# the recovery: the case asserts the refusal and its permitted neighbour
cat "$F/fixed-case.txt" > .sloprail/gate/demo/tests/inert/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "assert the refusal and the permit"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==2 and .[1].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
