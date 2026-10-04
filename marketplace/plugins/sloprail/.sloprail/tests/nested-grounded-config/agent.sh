#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$2" '{file_path:$p}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }

case $n in
  0) write_ c1 marketplace/plugins/x/config.yaml $'note: not a sloprail config\n' ;;
  1) write_ c2 marketplace/plugins/x/.sloprail/config.yaml $'disabled:\n  - sloprail/gate/no-lifecycle-commands\n' ;;
  *) finish ;;
esac
