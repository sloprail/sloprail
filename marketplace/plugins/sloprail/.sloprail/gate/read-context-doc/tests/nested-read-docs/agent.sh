#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
# It writes a look-alike OUTSIDE any .sloprail/ (not judged), loads the skill, then for each kind of
# declaration in a NESTED .sloprail/ tries to write it before reading its page (refused), reads the page,
# and writes it again (permitted).
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$DOCS/$2" '{file_path:$p}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }
DOCS="$SR_TEST_PLUGINS_DIR/sloprail/skills/authoring-guardrails"
N=marketplace/plugins/x

steps=("write_ b1 $N/gate/demo/gate.yaml \$'on:\n  - event: Stop\n'" "tu s1 Skill '{\"skill\":\"sloprail:authoring-guardrails\"}'")
add() { # id path content doc
  steps+=("write_ ${1}a $2 '$3'" "read_ ${1}r $4" "write_ ${1}b $2 '$3'")
}
add ctx    $N/.sloprail/context/demo/context.yaml    $'on: []\n' context.md
add fg     $N/.sloprail/file-guard/demo/file-guard.yaml $'match: "**"\n' file-guard.md
add gate   $N/.sloprail/gate/demo/gate.yaml          $'on:\n  - event: Stop\n' gate.md
add struct $N/.sloprail/file-guard/structure.yaml    $'scope:\n  - glob: "marketplace/plugins/x/"\nallow:\n  - glob: "marketplace/plugins/x/**"\n' structure-gate.md

if [ "$n" -lt "${#steps[@]}" ]; then eval "${steps[$n]}"; else finish; fi
