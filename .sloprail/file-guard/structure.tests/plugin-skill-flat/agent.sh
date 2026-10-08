#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }
S=marketplace/plugins/sloprail/skills
case $n in
  0) write_ w1 "$S/demo/SKILL.md" "# Demo" ;;
  1) write_ w2 "$S/demo/references/more.md" "# More" ;;
  2) write_ w3 "$S/README.md" "# Skills" ;;
  3) write_ w4 "$S/demo/more.md" "# More" ;;
  *) finish ;;
esac
