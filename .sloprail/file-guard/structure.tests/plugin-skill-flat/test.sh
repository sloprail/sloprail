#!/usr/bin/env bash
set -euo pipefail
# The project's structure gate on the sloprail plugin's skills: a skill is one flat folder,
# skills/<name>/<file>. Refused, each beside a permitted neighbour: a file in a subfolder of a
# skill, a file loose in skills/.
git init -q .
git add -A && git -c user.name=t -c user.email=t@t commit -q -m setup
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "write a skill")
outcomes() { echo "$RESULT" | jq -c '[.events[]|select(.kind=="StructureChecked" and .rule=="structure")|{id: .tool_use_id, o: .outcome}]'; }
echo "$RESULT" | jq -e '[.events[]|select(.kind=="StructureChecked" and .rule=="structure")] | map(.outcome)==["permitted","refused","refused","permitted"]' >/dev/null || { outcomes >&2; exit 1; }
S=marketplace/plugins/sloprail/skills
test -f "$S/demo/SKILL.md"
test -f "$S/demo/more.md"
test ! -e "$S/demo/references"
test ! -e "$S/README.md"
