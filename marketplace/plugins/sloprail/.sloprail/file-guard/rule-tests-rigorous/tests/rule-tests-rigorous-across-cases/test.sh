#!/usr/bin/env bash
set -euo pipefail
# rule-tests-rigorous, the subject is the RULE. The demo gate has two cases: A permits (it asserts only the
# permitted outcome), B refuses (it asserts only the refusal and its reason). Judged per case, each would be
# flagged (A has no refusal, B has no permit); judged as the rule's suite they are rigorous, and the judge is
# called ONCE for the rule (the mock logs its calls). Then: a rule changed alone judges the rule again; a
# sloppy third case fails the floor naming that case; a rule whose cases all refuse (no permit anywhere) is
# refused once, the missing permit listed; the permit restored passes.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
: > "$SR_EVENTS_FILE"
: > "$SR_EVENTS_FILE.judges"
mkdir -p .sloprail/gate/demo/tests .sloprail/file-guard
cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
printf '#!/usr/bin/env bash\necho "{\\"reason\\":\\"forbidden/ is read-only; write under allowed/ instead\\"}"\nexit 1\n' > .sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a demo gate"
BASE=$(git rev-parse HEAD)
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/rule-tests-rigorous/judge":"'"$SR_TEST_CASE_DIR"'/judge-rigor.sh"}'

# the permit in case A, the refusal in case B: one subject, one judge call, passed
mkdir -p .sloprail/gate/demo/tests/permit-neighbour .sloprail/gate/demo/tests/refusal-reason
cat "$F/permit-case.txt" > .sloprail/gate/demo/tests/permit-neighbour/test.sh
cat "$F/refusal-case.txt" > .sloprail/gate/demo/tests/refusal-reason/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "the permit and the refusal in two cases"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==1 and .[0].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
[ "$(cat "$SR_EVENTS_FILE.judges")" = "judged demo cases=2" ]

# the rule alone changes: the rule is judged again, once more
printf '# the refusal text\n' >> .sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "touch the rule"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==2 and .[1].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
[ "$(wc -l < "$SR_EVENTS_FILE.judges" | tr -d ' ')" = 2 ]

# a sloppy third case fails the floor, naming that case and only it; the judge is not asked
mkdir -p .sloprail/gate/demo/tests/sloppy
cat "$F/sloppy-case.txt" > .sloprail/gate/demo/tests/sloppy/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a sloppy case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==3 and .[2].outcome=="refused" and (.[2].reason|contains("gate/demo/tests/sloppy")) and (.[2].reason|contains("does not start with a shebang")) and (.[2].reason|contains("asserts no event of its owning rule demo")) and (.[2].reason|split("The files this refusal")[0]|contains("permit-neighbour") or contains("refusal-reason")|not)' "$SR_EVENTS_FILE" >/dev/null
[ "$(wc -l < "$SR_EVENTS_FILE.judges" | tr -d ' ')" = 2 ]

# no permit in any case: the rule is refused once, the missing permit listed
git rm -q -r .sloprail/gate/demo/tests/sloppy
git rm -q -r .sloprail/gate/demo/tests/permit-neighbour
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "drop the permit"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==4 and .[3].outcome=="refused" and (.[3].reason|contains("REFUSAL AND PERMIT: no case asserts a permit"))' "$SR_EVENTS_FILE" >/dev/null
[ "$(wc -l < "$SR_EVENTS_FILE.judges" | tr -d ' ')" = 3 ]

# the recovery the refusal asked for: a permit, in a case of its own
mkdir -p .sloprail/gate/demo/tests/permit-neighbour
cat "$F/permit-case.txt" > .sloprail/gate/demo/tests/permit-neighbour/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "restore the permit"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==5 and .[4].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
