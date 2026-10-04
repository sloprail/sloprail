#!/usr/bin/env bash
set -euo pipefail
# rule-tests-rigorous, the judge. The case passes the script floor (shebang, events, a reason asserted) but
# only ever asserts the refusal. The judge, mocked to decide from the case files in its input, refuses it for
# the missing permit; the agent adds the permit scenario (the same case, one more assertion) and it passes.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
mkdir -p .sloprail/gate/demo .sloprail/tests/demo-case .sloprail/file-guard
cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
printf '#!/usr/bin/env bash\necho "{\\"reason\\":\\"forbidden/ is read-only; write under allowed/ instead\\"}"\nexit 1\n' > .sloprail/gate/demo/refuse.sh
cp -R "$SR_TEST_SLOPRAIL_DIR/file-guard/rule-tests-rigorous" .sloprail/file-guard/
chmod -R u+w .sloprail/file-guard
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a demo gate"
BASE=$(git rev-parse HEAD)
export SR_CHECKS_JUDGE_MOCKS='{"file-guard/rule-tests-rigorous/judge":"'"$SR_TEST_CASE_DIR"'/judge-rigor.sh"}'

cat "$F/refusal-only-case.txt" > .sloprail/tests/demo-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a case that only refuses"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="rule-tests-rigorous")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("REFUSAL AND PERMIT")) and (.[0].reason|contains("permitted outcome"))' "$SR_EVENTS_FILE" >/dev/null

# the recovery: the permit scenario beside the refusal
cat "$F/with-permit-case.txt" > .sloprail/tests/demo-case/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "add the permit"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="rule-tests-rigorous")] | length==2 and .[1].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
