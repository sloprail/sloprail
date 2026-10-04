#!/usr/bin/env bash
set -euo pipefail

# A fixture a case commits is data, even when it holds a whole nested `.sloprail/` of its own: a structure.yaml
# one level too high INSIDE <rule>/tests/<case>/fixtures/.sloprail/ is not a misplaced declaration (the nearest
# `.sloprail/` is the fixture's, but the case folder above it exempts it). The same file straight under the
# project's own `.sloprail/` is refused naming where it belongs.
git init -q .
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "write a fixture, then the structure")
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/misplaced-declaration")] | length==2 and .[0].tool_use_id=="w1" and .[0].outcome=="permitted" and .[1].tool_use_id=="w2" and .[1].outcome=="refused" and (.[1].reason|contains(".sloprail/file-guard/structure.yaml"))' >/dev/null
test -f .sloprail/gate/demo/tests/c/fixtures/.sloprail/structure.yaml
test ! -e .sloprail/structure.yaml
