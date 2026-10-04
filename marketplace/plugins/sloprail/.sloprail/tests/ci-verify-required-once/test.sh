#!/usr/bin/env bash
set -euo pipefail

# a project with its own file-guards and no CI marker: Stop is refused once with the notice, then allowed
git init -q .
mkdir -p .sloprail/file-guard/demo
printf 'match: "**/*.txt"\nchecks:\n  - script: ./ok.sh\n' > .sloprail/file-guard/demo/file-guard.yaml
printf '#!/bin/sh\nexit 0\n' > .sloprail/file-guard/demo/ok.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m setup
RESULT=$(sr-test agent "$CASE/agent.sh" --prompt "finish")
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/ci-verify-required")] | .[0].outcome=="refused" and (.[0].reason|contains("sr:ci verify")) and .[-1].outcome=="permitted" and .[0].on=="Stop"' >/dev/null
echo "$RESULT" | jq -e '.exit==0' >/dev/null
