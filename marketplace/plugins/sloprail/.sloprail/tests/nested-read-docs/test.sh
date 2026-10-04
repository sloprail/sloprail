#!/usr/bin/env bash
set -euo pipefail

# declarations in a NESTED .sloprail/ need the authoring doc read first, like the root's; a look-alike outside
# any .sloprail/ does not, and after reading the doc the retry lands
git init -q .
mkdir -p .claude/skills/authoring-guardrails
for f in SKILL.md structure-gate.md gate.md context.md file-guard.md; do printf "# stub\n" > .claude/skills/authoring-guardrails/$f; done

RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "add the plugin's rules")
printf '%s' "$RESULT" | jq -e 'def g($n;$id): [.events[]|select(.kind=="GateChecked" and .rule==$n and .tool_use_id==$id)][0]; g("sloprail/read-gate-doc";"n1").outcome=="refused" and g("sloprail/read-context-doc";"n2").outcome=="refused" and g("sloprail/read-file-guard-doc";"n3").outcome=="refused" and g("sloprail/read-structure-gate-doc";"n4").outcome=="refused" and g("sloprail/read-gate-doc";"n5").outcome=="permitted" and (g("sloprail/read-gate-doc";"n1").reason|contains("authoring-guardrails"))' >/dev/null
printf '%s' "$RESULT" | jq -e '[.events[]|select(.tool_use_id=="b1")] | length==0' >/dev/null
test ! -e marketplace/plugins/x/.sloprail/context/demo/context.yaml
test -f marketplace/plugins/x/.sloprail/gate/demo/gate.yaml
test -f marketplace/plugins/x/gate/demo/gate.yaml
