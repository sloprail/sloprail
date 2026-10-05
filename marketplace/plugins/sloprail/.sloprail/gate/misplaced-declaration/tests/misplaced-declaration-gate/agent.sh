#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
fb=$(grep -c "Stop hook feedback" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); fb=${fb:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$DOCS/$2" '{file_path:$p}')"; }
DOCS="$SR_TEST_PLUGINS_DIR/sloprail/skills/authoring-guardrails"
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }


case $n in
  0) write_ w1 .sloprail/structure.yaml $'allow:\n  - glob: ".sloprail/**"\n' ;;
  1) tu s1 Skill '{"skill":"sloprail:authoring-guardrails"}' ;;
  2) read_ r1 structure-gate.md ;;
  3) write_ w3 .sloprail/file-guard/structure.yaml $'allow:\n  - glob: ".sloprail/**"\n' ;;
  4) write_ w2 .sloprail/gate/demo/data.yaml $'note: a data file a check reads\n' ;;
  *) finish ;;
esac
