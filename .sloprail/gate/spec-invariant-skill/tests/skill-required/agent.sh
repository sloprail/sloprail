#!/usr/bin/env bash
# A scripted agent: writes a spec invariant before loading document-invariant (refused), writes a
# neighbour outside spec/<domain>/invariants/ (not this gate's business), loads the skill, writes again.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
case $n in
  0) write_ wa spec/demo/invariants/a.yaml 'doc: d' ;;
  1) write_ xb spec/demo/README.md 'neighbour' ;;
  2) tu s1 Skill '{"skill":"document-invariant"}' ;;
  3) write_ wb spec/demo/invariants/a.yaml 'doc: d' ;;
  *) echo '{"type":"result","subtype":"success","result":"done","is_error":false}' ;;
esac
