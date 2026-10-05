#!/usr/bin/env bash
set -euo pipefail

git init -q .
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "write the structure")
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/misplaced-declaration")] | .[0].outcome=="refused" and .[0].tool_use_id=="w1" and (.[0].reason|contains(".sloprail/file-guard/structure.yaml")) and .[1].outcome=="permitted" and .[1].tool_use_id=="w3" and .[2].outcome=="permitted" and .[2].tool_use_id=="w2" and length==3' >/dev/null
test ! -e .sloprail/structure.yaml
test -f .sloprail/gate/demo/data.yaml
# the recovery: the same content, written where the reason names, is permitted and lands
test -f .sloprail/file-guard/structure.yaml
grep -qF 'glob: ".sloprail/**"' .sloprail/file-guard/structure.yaml
