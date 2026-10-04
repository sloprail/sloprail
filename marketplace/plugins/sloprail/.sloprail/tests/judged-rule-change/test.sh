#!/usr/bin/env bash
set -euo pipefail

# authoring-slop judge mocked: the first verdict fails, a re-judge passes
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
cat > "$CASE/judge-fail.sh" <<'SH'
#!/bin/sh
cat >/dev/null
echo '{"pass":false,"reasoning":"the hook script guesses instead of reading the event"}'
SH
chmod +x "$CASE/judge-fail.sh"
export SR_CHECKS_JUDGE_MOCKS='{"sloprail/file-guard/authoring-slop/judge":"'"$CASE"'/judge-fail.sh","sloprail/file-guard/grounded-rule-changes/judge":"'"$CASE"'/judge-fail.sh"}'
RESULT=$(sr-test agent "$CASE/agent.sh" --prompt "add a hook script")
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/authoring-slop")] | length>=1 and .[0].outcome=="refused" and (.[0].reason|contains("guesses instead of reading the event"))' >/dev/null
