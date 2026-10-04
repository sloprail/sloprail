#!/usr/bin/env bash
set -euo pipefail

# one command trips two gates at once: both refuse the same tool call
git init -q .
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "wait, then stop the session")
echo "$RESULT" | jq -e 'def ref($n): [.events[]|select(.kind=="GateChecked" and .rule==$n)][0] | .outcome=="refused" and .tool_use_id=="t1"; ref("sloprail/no-self-matching-pgrep") and ref("sloprail/no-lifecycle-commands")' >/dev/null
