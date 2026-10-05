#!/usr/bin/env bash
set -euo pipefail

# A git invocation whose subcommand (or an option in front of it) is hidden behind a variable or a
# substitution may be a push: the gate refuses it, fail closed, telling the agent to use the literal
# subcommand. The same holds behind a wrapper (`timeout $T`, `env -S "$A"`) and after a `builtin cd` /
# `command cd` to a folder the line never assigned. Nothing reaches the remote.
REMOTE="$(dirname "$SR_TEST_CASE_DIR")/remote.git"
git init -q --bare "$REMOTE"
git init -q -b main .
mkdir -p .sloprail
# the gate ships off (#243): this project opts in
printf 'enabled:\n  - sloprail/gate/verify-before-push\ndisabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m init
git remote add origin "$REMOTE"
git push -q origin main
git checkout -q -b feature
mkdir -p .sloprail/gate/demo
printf 'on:\n  - event: Stop\n' > .sloprail/gate/demo/gate.yaml
git add .sloprail/gate/demo/gate.yaml
git -c user.name=t -c user.email=t@t commit -q -m "add a gate"

RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "push the work")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/verify-before-push")|{outcome,tool_use_id,reason:(.reason//""|.[0:200])}' >&2; exit 1; }
verdict() { echo "$RESULT" | jq -e --arg id "$1" --arg out "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/verify-before-push" and .tool_use_id==$id)] | length==1 and .[0].outcome==$out' >/dev/null; }
reason_has() { echo "$RESULT" | jq -e --arg id "$1" --arg s "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/verify-before-push" and .tool_use_id==$id)][0] | (.reason|contains($s))' >/dev/null; }

for id in h1 h2 h3 h4; do
  verdict $id refused || fail "$id: a push with a hidden subcommand was not refused"
  reason_has $id "literal subcommand" || fail "$id: the refusal does not tell the agent to use the literal subcommand"
done
for id in h5 h6; do
  verdict $id refused || fail "$id: a push after a cd to an unassigned variable was not refused"
  reason_has $id "git -C <literal dir> push" || fail "$id: the refusal does not tell the agent to use a literal folder"
done
# recovery: the literal push is judged like any push (refused until verified), then permitted and lands
verdict p1 refused || fail "p1: the literal push of unverified commits was not refused"
reason_has p1 "sr-checks run --base" || fail "p1: the refusal does not name the run that judges the commits"
verdict p2 permitted || fail "p2: the literal push after the run was refused"
test "$(git -C "$REMOTE" rev-parse refs/heads/feature)" = "$(git rev-parse HEAD)" || fail "the remote's feature is not HEAD"
