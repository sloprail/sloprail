#!/usr/bin/env bash
set -euo pipefail

# disabling a rule in a NESTED .sloprail/config.yaml is a rule change: refused until cited, like the root's;
# a config.yaml outside any .sloprail/ is not
git init -q .
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "turn the lifecycle rule off")
printf '%s' "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/grounded-rule-changes")] | .[0].tool_use_id=="c2" and .[0].outcome=="refused"' >/dev/null
test ! -e marketplace/plugins/x/.sloprail/config.yaml
test -f marketplace/plugins/x/config.yaml
