#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }

case $n in
  0) write_ w1 tests/e2e/harness/area/001_drives/a_test.go "package x" ;;
  1) write_ w2 tests/e2e/area/001_nolayer/a_test.go "package x" ;;
  2) write_ w3 tests/e2e/cli/area/a_test.go "package x" ;;
  3) write_ w4 tests/e2e/cli/area/002_builds/sub/a_test.go "package x" ;;
  4) write_ w5 tests/e2e/cli/area/002_builds/a_test.go "package x" ;;
  *) finish ;;
esac
