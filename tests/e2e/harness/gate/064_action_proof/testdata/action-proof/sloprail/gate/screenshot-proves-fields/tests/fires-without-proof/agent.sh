#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }
case $n in
  0) tu f1 mcp__browser__fill_form '{"name":"Ada Lovelace","email":"ada@example.com","mock_result":{"content":[{"type":"text","text":"Filled 2 fields"}],"isError":false}}' ;;
  *) finish ;;
esac
