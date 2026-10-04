#!/usr/bin/env bash
set -euo pipefail

# a shell redirect is invisible to the gate: the misplaced declaration is committed, refused at Stop, moved
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
RESULT=$(sr-test agent "$CASE/agent.sh" --prompt "write the structure one level too high")
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/misplaced-declaration")] | (.[0].outcome=="refused") and (.[0].reason|contains(".sloprail/file-guard/structure.yaml")) and (.[-1].outcome=="passed")' >/dev/null
test -f .sloprail/file-guard/structure.yaml
test ! -e .sloprail/structure.yaml
