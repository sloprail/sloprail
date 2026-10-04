#!/usr/bin/env bash
set -euo pipefail

# Each read-*-doc gate refuses the write of its kind of declaration until the page for it was read,
# naming the page to read. The skill being loaded, and other pages read, is not enough (the boundary):
# the page for THIS kind is what is checked. After reading it, the same write is permitted.
git init -q .
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "write one of each kind of declaration")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.kind=="GateChecked")|{rule,outcome,tool_use_id,reason}' >&2; exit 1; }

check() { # rule id page file
  echo "$RESULT" | jq -e --arg r "sloprail/$1" --arg id "$2" --arg page "$3" '
    [.events[]|select(.kind=="GateChecked" and .rule==$r and .tool_use_id==($id+"a"))] as $a
    | [.events[]|select(.kind=="GateChecked" and .rule==$r and .tool_use_id==($id+"b"))] as $b
    | ($a|length)==1 and $a[0].outcome=="refused" and ($a[0].reason|contains("READ REQUIRED") and contains($page))
      and ($b|length)==1 and $b[0].outcome=="permitted"' >/dev/null || fail "$1: expected a refusal naming $3, then a permit after reading it"
  test -f "$4" || fail "$1: $4 was not written after the page was read"
}
check read-context-doc      ctx    context.md          .sloprail/context/demo/context.yaml
check read-file-guard-doc   fg     file-guard.md       .sloprail/file-guard/demo/file-guard.yaml
check read-gate-doc         gate   gate.md             .sloprail/gate/demo/gate.yaml
check read-judge-checks-doc judge  judge-checks.md     .sloprail/gate/demo/judge.md.j2
check read-script-checks-doc script script-checks.md   .sloprail/gate/demo/check.sh
check read-structure-gate-doc struct structure-gate.md .sloprail/file-guard/structure.yaml
# the script gate names BOTH its pages
echo "$RESULT" | jq -e '[.events[]|select(.rule=="sloprail/read-script-checks-doc" and .tool_use_id=="scripta")][0].reason|contains("check-template.sh")' >/dev/null || fail "read-script-checks-doc did not name check-template.sh"
