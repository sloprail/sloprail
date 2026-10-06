#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines. The main
# session dispatches a sub-agent that runs sub.sh, then runs sr-checks itself.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }

case $n in
  0) tu t1 Agent "$(jq -nc --arg s "$SR_TEST_CASE_DIR/sub.sh" '{description:"delegated work",prompt:"judge the range",subagent_type:"general-purpose",script:$s}')" ;;
  1) bash_ t2 "sr-checks run --base HEAD --head HEAD" ;;
  *) finish ;;
esac
