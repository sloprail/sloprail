#!/usr/bin/env bash
set -euo pipefail

# Declarations in a NESTED .sloprail/ need the page for their kind read first, like the root's: each of the four
# read-*-doc gates refuses naming its page, and after reading it the same write is permitted. A look-alike
# outside any .sloprail/ is not judged at all.
git init -q .
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "add the plugin's rules")
fail() { echo "$1" >&2; printf '%s' "$RESULT" | jq -c '.events[]|select(.kind=="GateChecked")|{rule,outcome,tool_use_id,reason}' >&2; exit 1; }

check() { # rule id page file
  printf '%s' "$RESULT" | jq -e --arg r "sloprail/$1" --arg id "$2" --arg page "$3" '
    [.events[]|select(.kind=="GateChecked" and .rule==$r and .tool_use_id==($id+"a"))] as $a
    | [.events[]|select(.kind=="GateChecked" and .rule==$r and .tool_use_id==($id+"b"))] as $b
    | ($a|length)==1 and $a[0].outcome=="refused" and ($a[0].reason|contains("READ REQUIRED") and contains($page))
      and ($b|length)==1 and $b[0].outcome=="permitted"' >/dev/null || fail "$1: expected a refusal naming $3, then a permit after reading it"
  test -f "$4" || fail "$1: $4 was not written after the page was read"
}
N=marketplace/plugins/x
check read-context-doc        ctx    context.md        $N/.sloprail/context/demo/context.yaml
check read-file-guard-doc     fg     file-guard.md     $N/.sloprail/file-guard/demo/file-guard.yaml
check read-gate-doc           gate   gate.md           $N/.sloprail/gate/demo/gate.yaml
check read-structure-gate-doc struct structure-gate.md $N/.sloprail/file-guard/structure.yaml
# near boundary: the same shape outside any .sloprail/ raised no verdict at all, and landed
printf '%s' "$RESULT" | jq -e '[.events[]|select(.tool_use_id=="b1")] | length==0' >/dev/null || fail "a path outside .sloprail/ was judged"
test -f $N/gate/demo/gate.yaml || fail "the look-alike outside .sloprail/ was not written"
