#!/usr/bin/env bash
set -euo pipefail

# a structure.yaml one level too high in a NESTED .sloprail/ is refused naming the nested root; the corrected path,
# a data file in a rule folder and a structure.yaml outside any .sloprail/ is not even judged
git init -q .
mkdir -p .claude/skills/authoring-guardrails
for f in SKILL.md structure-gate.md gate.md context.md file-guard.md; do printf "# stub\n" > .claude/skills/authoring-guardrails/$f; done

RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "add the plugin's structure")
printf '%s' "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/misplaced-declaration")] | .[0].tool_use_id=="w1" and .[0].outcome=="refused" and (.[0].reason|contains("marketplace/plugins/x/.sloprail/file-guard/structure.yaml")) and (.[1:]|map(.outcome)|all(.=="permitted"))' >/dev/null
printf '%s' "$RESULT" | jq -e '[.events[]|select(.kind=="GateChecked" and .tool_use_id=="w4")] | length==0' >/dev/null
test ! -e marketplace/plugins/x/.sloprail/structure.yaml
test -f marketplace/plugins/x/.sloprail/file-guard/structure.yaml
test -f marketplace/plugins/x/.sloprail/gate/demo/data.yaml
test -f marketplace/plugins/x/structure.yaml
