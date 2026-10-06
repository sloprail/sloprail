#!/usr/bin/env bash
# The sub-agent: tries to judge (refused), then verifies (asks no model, not matched), then ends.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }

case $n in
  0) bash_ s1 "sr-checks run --base HEAD --head HEAD" ;;
  1) bash_ s2 "sr checks run --base HEAD --head HEAD" ;;
  2) bash_ s3 "sr-checks verify --base HEAD --head HEAD" ;;
  *) finish ;;
esac
