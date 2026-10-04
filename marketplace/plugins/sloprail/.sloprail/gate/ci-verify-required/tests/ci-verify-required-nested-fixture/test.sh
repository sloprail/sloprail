#!/usr/bin/env bash
set -euo pipefail

# a repository whose only file-guard-looking files are fixtures inside sr-test case folders (a nested
# `.sloprail/file-guard/` below a gate's tests/, and one below a file-guard rule's tests/) has no file-guards
# of its own: Stop is not refused for a missing CI marker
git init -q .
mkdir -p .sloprail/gate/g/tests/c/fixtures/.sloprail/file-guard/y
mkdir -p .sloprail/file-guard/r/tests/c/.sloprail/file-guard/z
printf 'match: "**/*.txt"\nchecks:\n  - script: ./ok.sh\n' > .sloprail/gate/g/tests/c/fixtures/.sloprail/file-guard/y/file-guard.yaml
printf 'match: "**/*.txt"\nchecks:\n  - script: ./ok.sh\n' > .sloprail/file-guard/r/tests/c/.sloprail/file-guard/z/file-guard.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m setup
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "finish")
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/ci-verify-required")] | length >= 1 and all(.[]; .outcome=="permitted")' >/dev/null
echo "$RESULT" | jq -e '.exit==0' >/dev/null

# the control: a file-guard of the project's own (not a fixture) is still counted, so the same Stop is refused for
# the missing CI marker, with the notice
mkdir -p .sloprail/file-guard/demo
printf 'match: "**/*.txt"\nchecks:\n  - script: ./ok.sh\n' > .sloprail/file-guard/demo/file-guard.yaml
printf '#!/bin/sh\nexit 0\n' > .sloprail/file-guard/demo/ok.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a real file-guard"
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "finish again")
echo "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/ci-verify-required")] | .[0].outcome=="refused" and (.[0].reason|contains("sr:ci verify")) and .[0].on=="Stop"' >/dev/null
