#!/usr/bin/env bash
set -euo pipefail

# judged-content-is-data names every harness's non-interactive CLI, not only claude's: a hook that calls the
# codex or cursor CLI directly and never says the content is DATA, never as instructions, is refused, while the
# same call through sr-agent (with that clause) passes. This script invokes no model.
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "add judge scripts")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/authoring-slop")|{kind,outcome,on,tool_use_id,reason}' >&2; exit 1; }
SESSION=$(echo "$RESULT" | jq -er .session)
OUT=$(jq -rs '[.[]|select(.type=="user")|.message.content[]?|select(.type=="tool_result" and .tool_use_id=="c1")][0]|[.is_error, (.content|if type=="array" then map(.text)|join("") else . end)]|@json' "$SESSION")
echo "$OUT" | jq -e '.[0]==true and (.[1]|contains("judged-content-is-data"))' >/dev/null || fail "c1: the direct calls were not refused: $OUT"
echo "$OUT" | jq -e '.[1]|contains("codex.sh")' >/dev/null || fail "c1: the direct codex call was not named: $OUT"
echo "$OUT" | jq -e '.[1]|contains("cursor.sh")' >/dev/null || fail "c1: the direct cursor call was not named: $OUT"
echo "$OUT" | jq -e '.[1]|contains("via-sr-agent.sh")|not' >/dev/null || fail "c1: the sr-agent call with a DATA clause was refused: $OUT"
