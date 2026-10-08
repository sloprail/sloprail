#!/usr/bin/env bash
# A scripted agent: a 201-line skill file (refused), the same file at 200 lines (permitted), and a
# 201-line file outside the plugin's skills (not this gate's business).
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
S=marketplace/plugins/sloprail/skills/demo
case $n in
  0) write_ w1 "$S/SKILL.md" "$(seq 201)" ;;
  1) write_ w2 "$S/SKILL.md" "$(seq 200)" ;;
  2) write_ x1 docs/long.md "$(seq 201)" ;;
  *) echo '{"type":"result","subtype":"success","result":"done","is_error":false}' ;;
esac
