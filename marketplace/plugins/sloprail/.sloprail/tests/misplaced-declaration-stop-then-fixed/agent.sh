#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
fb=$(grep -c "Stop hook feedback" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); fb=${fb:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }


case $n in
  0) bash_ b1 "mkdir -p .sloprail && printf 'allow:\\n  - glob: \".sloprail/**\"\\n' > .sloprail/structure.yaml && git add -A && git -c user.name=t -c user.email=t@t commit -q -m structure" ;;
esac
if [ "$n" -ge 1 ] && [ "$fb" -eq 0 ]; then finish; exit 0; fi
if [ "$n" -ge 1 ] && [ "$fb" -ge 1 ] && [ "$n" -lt 2 ]; then
  bash_ b2 "mkdir -p .sloprail/file-guard && git mv .sloprail/structure.yaml .sloprail/file-guard/structure.yaml && git -c user.name=t -c user.email=t@t commit -q -m move-structure"
elif [ "$n" -ge 2 ]; then finish; fi
