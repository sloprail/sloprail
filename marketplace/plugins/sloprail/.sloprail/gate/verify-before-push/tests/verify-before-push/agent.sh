#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
fb=$(grep -c "Stop hook feedback" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); fb=${fb:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }


# The refusal names the `sr-checks run --base <b> --head <h>` that judges the range: the agent runs it, then pushes again.
cmd=$(grep -o 'sr-checks run --base [0-9a-f]* --head [0-9a-f]*' "$A10N_MOCK_SESSION_FILE" 2>/dev/null | tail -1)
case $n in
  0) bash_ p1 "git push origin HEAD" ;;
  1) if [ -n "$cmd" ]; then bash_ c1 "$cmd"; else finish; fi ;;
  2) bash_ p2 "git push origin HEAD" ;;
  *) finish ;;
esac
