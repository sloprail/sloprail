#!/usr/bin/env bash
set -euo pipefail

git init -q .
mkdir -p .sloprail/file-guard
printf 'allow:\n  - glob: ".sloprail/**"\n  - glob: "src/**"\n' > .sloprail/file-guard/structure.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m setup
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "write two files")
# the structure gate's events: an allowed path is permitted, a stray one refused for the deny-by-default
# reason with its recovery (write under an allowed path), and the agent that recovers is permitted
echo "$RESULT" | jq -e '[.events[]|select(.kind=="StructureChecked" and .rule=="structure")] | map(.outcome)==["permitted","refused","permitted"] and .[1].tool_use_id=="w2" and (.[1].reason|contains("deny-by-default")) and (.[1].reason|contains("Write under an allowed path")) and .[2].tool_use_id=="w3"' >/dev/null
test -f src/app.txt
test -f src/notes.txt
test ! -e docs/notes.txt
