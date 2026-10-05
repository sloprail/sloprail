#!/usr/bin/env bash
set -euo pipefail

# A git invocation whose subcommand (or an option in front of it) is a variable or a substitution the
# line never assigned could be a write to the results ref that names nothing in its argv: the gate
# refuses it, fail closed, telling the agent to use the literal subcommand, through a wrapper too. The
# literal form of a read is permitted.
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "look around")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/checks-ref-sr-only")|{outcome,tool_use_id,reason:(.reason//""|.[0:300])}' >&2; exit 1; }
verdict() { echo "$RESULT" | jq -e --arg id "$1" --arg out "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/checks-ref-sr-only" and .tool_use_id==$id)] | length==1 and .[0].outcome==$out' >/dev/null; }
reason_has() { echo "$RESULT" | jq -e --arg id "$1" --arg s "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/checks-ref-sr-only" and .tool_use_id==$id)][0] | (.reason|contains($s))' >/dev/null; }

for id in h1 h2 h3 h4 h5; do
  verdict $id refused || fail "$id: a git command with a hidden subcommand was not refused"
  reason_has $id "literal subcommand" || fail "$id: the refusal does not tell the agent to use the literal subcommand"
done
# recovery: the ref written literally is decided: the read is permitted, the write refused as the results-ref reason
verdict r1 permitted || fail "r1: the literal read of the results ref was refused"
verdict r2 refused || fail "r2: the literal write of the results ref was not refused"
reason_has r2 "only sr-checks writes it" || fail "r2: the refusal is not the results-ref reason"
verdict l1 permitted || fail "l1: the literal read was refused"
