#!/usr/bin/env bash
set -euo pipefail

# the CI path, no agent: sr-checks run judges a committed range
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
mkdir -p .sloprail
printf 'allow:\n  - glob: ".sloprail/**"\n' > .sloprail/structure.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m misplaced
if sr-checks run --base HEAD~1 --head HEAD >/dev/null 2>&1; then echo "the misplaced declaration passed" >&2; exit 1; fi
jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="sloprail/misplaced-declaration" and .outcome=="refused" and (.reason|contains(".sloprail/file-guard/structure.yaml")))' "$SR_EVENTS_FILE" >/dev/null
