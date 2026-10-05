#!/usr/bin/env bash
set -euo pipefail

# A git command that could name the results ref through a word the line never assigned (a variable as the
# subcommand or as a ref) is refused with the reason that tells the agent to write it out literally, instead of
# being read with the word dropped. A variable the line assigns to the literal ref is that ref: a write through
# it is refused like the literal write, a read through it is permitted, and so is the literal read after the refusal.
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "look at the checks ref")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/checks-ref-sr-only")|{outcome,tool_use_id,reason:(.reason//""|.[0:300])}' >&2; exit 1; }
has() { case "$1" in *"$2"*) return 0 ;; *) return 1 ;; esac; }
verdict() { echo "$RESULT" | jq -e --arg id "$1" --arg out "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/checks-ref-sr-only" and .tool_use_id==$id)] | length==1 and .[0].outcome==$out' >/dev/null; }
reason() { echo "$RESULT" | jq -r --arg id "$1" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/checks-ref-sr-only" and .tool_use_id==$id)][0].reason'; }
# the refusal's own sentence, read from the owner's event (the reason is what says WHY the rule refused)
reason_has() { echo "$RESULT" | jq -e --arg id "$1" --arg s "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/checks-ref-sr-only" and .tool_use_id==$id)][0] | (.reason|contains($s))' >/dev/null; }

verdict u1 refused || fail "u1: a git command with an unresolvable subcommand was not refused"
reason_has u1 "Write it out literally" || fail "u1: the refusal does not tell the agent to write it literally"
verdict u2 refused || fail "u2: a push naming a ref through an unresolvable variable was not refused"
reason_has u2 "Write the ref out literally" || fail "u2: the refusal does not tell the agent to write the ref literally"
verdict v1 refused || fail "v1: a write through R=<the results ref> was not refused"
reason_has v1 "only sr-checks writes it" || fail "v1: the refusal is not the results-ref reason"
verdict v2 permitted || fail "v2: a read through R=<the results ref> was refused"
verdict l1 permitted || fail "l1: the literal read after the refusal was refused"
# the refused write never moved the ref
test -z "$(git for-each-ref refs/sloprail/checks)" || fail "the results ref exists: a refused write landed"
