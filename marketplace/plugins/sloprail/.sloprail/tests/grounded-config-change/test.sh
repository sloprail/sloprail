#!/usr/bin/env bash
set -euo pipefail

# The PreFileWrite half of grounded-rule-changes: writing .sloprail/config.yaml (a `disabled:` list switches
# rules off) must cite the user's words or a tool's output. The Write tool carries no citation, so it is
# refused with how to cite; `sr-file write ... --cite:user '<quote>'` with the user's real words is permitted
# and lands. A neighbouring file under .sloprail/ is not this gate's business.
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "turn off the pgrep gate for this project")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/grounded-rule-changes")|{kind,rule,outcome,on,tool_use_id,reason}' >&2; exit 1; }

echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/grounded-rule-changes" and .tool_use_id=="w1")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("Sloprail-Cites-User") or contains("sr-file") or contains("cite"))
  and (.[0].reason|contains("Never disable, loosen or delete a rule"))' >/dev/null || fail "w1: the uncited config.yaml write was not refused with how to ground it"
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/grounded-rule-changes" and .tool_use_id=="s1")] | length==1 and .[0].outcome=="permitted"' >/dev/null || fail "s1: sr-file write with the user's words was not permitted"
echo "$RESULT" | jq -e '[.events[]|select(.rule=="sloprail/grounded-rule-changes" and .tool_use_id=="w2")] | length==0' >/dev/null || fail "w2: a neighbouring file was this gate's business"
grep -q 'no-self-matching-pgrep' .sloprail/config.yaml || fail "config.yaml was not written by the cited sr-file write"
