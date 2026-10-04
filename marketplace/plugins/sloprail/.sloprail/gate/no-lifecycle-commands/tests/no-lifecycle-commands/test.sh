#!/usr/bin/env bash
set -euo pipefail

# An agent running a harness hook entry point by hand (sr-session stop) is refused with the remedy;
# the read-only command it is told to use instead (sr-session trajectory) is not stopped.
git init -q .
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "check the session")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|{kind,rule,outcome,on,tool_use_id,reason}' >&2; exit 1; }

# the hook entry point is refused, naming what to do instead
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/no-lifecycle-commands")] | length==1 and .[0].outcome=="refused" and .[0].tool_use_id=="t1" and (.[0].reason|contains("sr-checks run --base")) and (.[0].reason|contains("sr-session trajectory"))' >/dev/null ||
  fail "sr-session stop was not refused with its remedy"

# the permit side: sr-session trajectory does not match the gate (it emits nothing), so what can fail is the
# call itself: t2 ran, its tool_result is not an error, and no refusal of this rule names t2
SESSION=$(echo "$RESULT" | jq -er .session)
jq -es 'any(.[]; .type=="user" and any(.message.content[]?; .type=="tool_result" and .tool_use_id=="t2" and (.is_error|not)))' "$SESSION" >/dev/null ||
  fail "sr-session trajectory --help (t2) did not run to a successful tool_result"
echo "$RESULT" | jq -e '[.events[]|select(.rule=="sloprail/no-lifecycle-commands" and .outcome=="refused" and .tool_use_id=="t2")] | length==0' >/dev/null ||
  fail "sr-session trajectory --help (t2) was refused by no-lifecycle-commands"
# and t1 never ran: its tool_result is the refusal
jq -es 'any(.[]; .type=="user" and any(.message.content[]?; .type=="tool_result" and .tool_use_id=="t1" and .is_error==true))' "$SESSION" >/dev/null ||
  fail "the refused sr-session stop (t1) has no error tool_result"
