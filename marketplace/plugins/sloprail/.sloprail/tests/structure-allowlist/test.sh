#!/usr/bin/env bash
set -euo pipefail

git init -q .
mkdir -p .sloprail/file-guard
printf 'allow:\n  - glob: ".sloprail/**"\n  - glob: "src/**"\n' > .sloprail/file-guard/structure.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m setup
RESULT=$(sr-test agent "$CASE/agent.sh" --prompt "write two files")
echo "$RESULT" | jq -e '[.events[]|select(.kind=="StructureChecked")] | .[0].outcome=="permitted" and .[1].outcome=="refused"' >/dev/null
test -f src/app.txt
test ! -e docs/notes.txt
