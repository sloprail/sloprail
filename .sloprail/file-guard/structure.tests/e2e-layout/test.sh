#!/usr/bin/env bash
set -euo pipefail
# The project's structure gate on tests/e2e: a package lands only at
# tests/e2e/<layer>/<area>/<NNN_name>/<file>. Refused, each beside a permitted
# neighbour: no layer, a file at area level, a file nested below the package.
git init -q .
git add -A && git -c user.name=t -c user.email=t@t commit -q -m setup
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "write e2e tests")
outcomes() { echo "$RESULT" | jq -c '[.events[]|select(.kind=="StructureChecked" and .rule=="structure")|{id: .tool_use_id, o: .outcome}]'; }
echo "$RESULT" | jq -e '[.events[]|select(.kind=="StructureChecked" and .rule=="structure")] | map(.outcome)==["permitted","refused","refused","refused","permitted"]' >/dev/null || { outcomes >&2; exit 1; }
test -f tests/e2e/harness/area/001_drives/a_test.go
test -f tests/e2e/cli/area/002_builds/a_test.go
test ! -e tests/e2e/area
test ! -e tests/e2e/cli/area/a_test.go
test ! -e tests/e2e/cli/area/002_builds/sub
