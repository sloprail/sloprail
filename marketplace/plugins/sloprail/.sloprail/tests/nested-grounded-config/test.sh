#!/usr/bin/env bash
set -euo pipefail

# disabling a rule in a NESTED .sloprail/config.yaml is a rule change: refused until cited, like the root's;
# a config.yaml outside any .sloprail/ is not
git init -q .
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "turn the lifecycle rule off")
printf '%s' "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/grounded-rule-changes")] | .[0].tool_use_id=="c2" and .[0].outcome=="refused" and (.[0].reason|contains("Never disable, loosen or delete a rule"))' >/dev/null
# the config.yaml outside any .sloprail/ is not this gate's business: no decision at all
printf '%s' "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/grounded-rule-changes" and .tool_use_id=="c1")] | length==0' >/dev/null
# recovery: the same change cited to the user's words (sr-file write --cite:user) is permitted, and lands
printf '%s' "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/grounded-rule-changes" and .tool_use_id=="s1")] | length==1 and .[0].outcome=="permitted"' >/dev/null
grep -q 'no-lifecycle-commands' marketplace/plugins/x/.sloprail/config.yaml
test -f marketplace/plugins/x/config.yaml
