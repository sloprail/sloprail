#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
# It loads the authoring-guardrails skill, then for each kind of declaration tries to write one before
# reading the page for its nature (refused: the skill and other pages are already read, not this one),
# reads the page(s), and writes it again.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$DOCS/$2" '{file_path:$p}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }
DOCS="$SR_TEST_PLUGINS_DIR/sloprail/skills/authoring-guardrails"

steps=("tu s1 Skill '{\"skill\":\"sloprail:authoring-guardrails\"}'")
add() { # id path content doc...
  local id=$1 path=$2 content=$3; shift 3
  steps+=("write_ ${id}a $path '$content'")
  local d; for d in "$@"; do steps+=("read_ ${id}r-$d $d"); done
  steps+=("write_ ${id}b $path '$content'")
}
add ctx    .sloprail/context/demo/context.yaml    $'on: []\n' context.md
add fg     .sloprail/file-guard/demo/file-guard.yaml $'match: "**"\n' file-guard.md
add gate   .sloprail/gate/demo/gate.yaml          $'on:\n  - event: Stop\n' gate.md
add judge  .sloprail/gate/demo/judge.md.j2        $'Judge it.\n' judge-checks.md
add script .sloprail/gate/demo/check.sh           $'#!/usr/bin/env bash\nexit 0\n' script-checks.md check-template.sh
add struct .sloprail/file-guard/structure.yaml    $'allow:\n  - glob: ".sloprail/**"\n' structure-gate.md

if [ "$n" -lt "${#steps[@]}" ]; then eval "${steps[$n]}"; else finish; fi
