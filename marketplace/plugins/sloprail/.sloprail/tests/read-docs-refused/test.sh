#!/usr/bin/env bash
set -euo pipefail

# writing a declaration before reading the authoring-guardrails doc for its nature is refused
git init -q .
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "add three rules")
echo "$RESULT" | jq -e 'def r($n;$id): [.events[]|select(.kind=="GateChecked" and .rule==$n and .tool_use_id==$id)][0].outcome=="refused"; r("sloprail/read-gate-doc";"r1") and r("sloprail/read-context-doc";"r2") and r("sloprail/read-file-guard-doc";"r3")' >/dev/null
test ! -e .sloprail/gate/demo/gate.yaml
