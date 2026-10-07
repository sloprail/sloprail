#!/usr/bin/env bash
set -euo pipefail
# rule-tests-rigorous, the script floor, on a LARGE valid case. The floor used to grep the case's body with
# `printf '%s\n' "$body" | grep -q ...` under pipefail: when the body is bigger than the pipe buffer (64KB) and
# grep -q matches in the first chunk, grep exits while printf is still writing, printf dies of SIGPIPE, the
# pipeline returns non-zero, and a valid case was refused (it flaked under parallel load; a big body makes it
# deterministic). The case below is the fixed one from the sibling case, its assertions first, followed by
# ~300KB of non-comment padding: the rule must PASS it.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/gate/demo/tests/demo-case .sloprail/file-guard
cat "$F/demo-gate.yaml" > .sloprail/gate/demo/gate.yaml
printf '#!/usr/bin/env bash\necho "{\\"reason\\":\\"forbidden/ is read-only; write under allowed/ instead\\"}"\nexit 1\n' > .sloprail/gate/demo/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a demo gate"
BASE=$(git rev-parse HEAD)
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/rule-tests-rigorous/judge":"'"$SR_TEST_CASE_DIR"'/judge-rigor.sh"}'

{
  cat "$F/fixed-case.txt"
  i=0
  while [ "$i" -lt 4000 ]; do
    printf 'echo "padding line %05d so that this case is far larger than the 64KB pipe buffer"\n' "$i"
    i=$((i + 1))
  done
} > .sloprail/gate/demo/tests/demo-case/test.sh
[ "$(wc -c < .sloprail/gate/demo/tests/demo-case/test.sh)" -gt 200000 ]
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a large valid case"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
# other rules of the plugin judge this range too (their verdicts are not this case's business): only this rule's event is asserted
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-rigorous")] | length==1 and .[0].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
