#!/usr/bin/env bash
set -euo pipefail

# A git invocation whose subcommand (or an option in front of it) is hidden behind a variable or a
# substitution may be a commit: the gate refuses it, fail closed, telling the agent to use the literal
# subcommand. The same holds behind a wrapper (`timeout $T`, `env -S "$A"`) and after a `builtin cd` /
# `command cd` to a folder the line never assigned. Nothing is committed.
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "make the commit")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/cite-before-commit")|{outcome,tool_use_id,reason:(.reason//""|.[0:300])}' >&2; exit 1; }
verdict() { echo "$RESULT" | jq -e --arg id "$1" --arg out "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/cite-before-commit" and .tool_use_id==$id)] | length==1 and .[0].outcome==$out' >/dev/null; }
reason_has() { echo "$RESULT" | jq -e --arg id "$1" --arg s "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/cite-before-commit" and .tool_use_id==$id)][0] | (.reason|contains($s))' >/dev/null; }

for id in h1 h2; do
  verdict $id refused || fail "$id: a commit with a hidden subcommand was not refused"
  reason_has $id "literal subcommand" || fail "$id: the refusal does not tell the agent to use the literal subcommand"
done
for id in h3 h4 h5 h6 h7; do
  verdict $id refused || fail "$id: a commit after a cd to an unassigned variable was not refused"
  reason_has $id "could not check" || fail "$id: the refusal does not say the commit could not be checked"
done
verdict l1 permitted || fail "l1: the literal commit was refused"
# only the literal commit landed
test "$(git rev-list --count HEAD)" = 2 || fail "a refused commit landed, or the literal one did not"
