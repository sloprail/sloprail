#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
fb=$(grep -c "Stop hook feedback" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); fb=${fb:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }


case $n in
  # a subcommand that is an unresolvable variable could be anything, the results ref's writer included: refused, write it literally
  0) bash_ u1 'git $UNSET_SUB update-ref refs/sloprail/checks HEAD' ;;
  # a ref that is an unresolvable variable cannot be checked against the results ref either
  1) bash_ u2 'git push --mirror origin $UNSET_REF' ;;
  # a variable the line assigns to a literal IS that ref: the write is refused as the literal form is, the read is permitted
  2) bash_ v1 'R=refs/sloprail/checks; git update-ref $R HEAD' ;;
  3) bash_ v2 'R=refs/sloprail/checks; git show-ref $R' ;;
  # recovery: the literal form of the read is decided and permitted
  4) bash_ l1 'git show-ref sloprail/checks' ;;
  *) finish ;;
esac
