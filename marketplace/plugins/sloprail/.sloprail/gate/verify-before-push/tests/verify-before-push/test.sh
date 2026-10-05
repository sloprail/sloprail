#!/usr/bin/env bash
set -euo pipefail

# A push of commits no `sr-checks run` judged is refused, naming the exact run to make; after the agent
# runs it, the same push (same command, same commits) is permitted and lands on the remote.
REMOTE="$(dirname "$SR_TEST_CASE_DIR")/remote.git"
git init -q --bare "$REMOTE"
git init -q -b main .
# the demo project's rules and structure are incidental here, not under test: rule-tests-pass would ask for a case for each
mkdir -p .sloprail
# the gate ships off (#243): this project opts in
printf 'enabled:\n  - sloprail/gate/verify-before-push\ndisabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m init
git remote add origin "$REMOTE"
git push -q origin main
git checkout -q -b feature
# a guarded file (a declaration under .sloprail/) rides on the branch, so the push has something to verify
mkdir -p .sloprail/gate/demo
printf 'on:\n  - event: Stop\n' > .sloprail/gate/demo/gate.yaml
git add .sloprail/gate/demo/gate.yaml
git -c user.name=t -c user.email=t@t commit -q -m "add a gate"

RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "push the work")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.kind!="GateChecked" or .rule!="sloprail/ci-verify-required")|{kind,rule,outcome,on,tool_use_id,reason}' >&2; exit 1; }

echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/verify-before-push")] | length==2
  and .[0].tool_use_id=="p1" and .[0].outcome=="refused" and (.[0].reason|contains("sr-checks run --base") and contains("--head"))
  and .[1].tool_use_id=="p2" and .[1].outcome=="permitted"' >/dev/null || fail "expected refused (p1) naming sr-checks run, then permitted (p2)"
# the first push never happened, the second landed
test "$(git -C "$REMOTE" rev-parse refs/heads/feature)" = "$(git rev-parse HEAD)" || fail "the remote's feature is not HEAD"
SESSION=$(echo "$RESULT" | jq -er .session)
jq -es 'any(.[]; .type=="user" and any(.message.content[]?; .type=="tool_result" and .tool_use_id=="p1" and .is_error==true))' "$SESSION" >/dev/null || fail "the refused push (p1) has no error tool_result"
jq -es 'any(.[]; .type=="user" and any(.message.content[]?; .type=="tool_result" and .tool_use_id=="c1" and (.is_error|not)))' "$SESSION" >/dev/null || fail "sr-checks run (c1) did not succeed"
