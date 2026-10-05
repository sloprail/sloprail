#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }

# The subcommand is hidden, and the ref (if any) is not on the line at all, so the argv names nothing to
# match on: only the gap in the command tells the gate it may be a write to the results ref.
case $n in
  0) bash_ h1 'git $UNSET_SUB' ;;
  1) bash_ h2 'git "$(echo update-ref)" refs/heads/x HEAD' ;;
  2) bash_ h3 'timeout $UNSET_T git status' ;;
  3) bash_ h4 'env $UNSET_E git status' ;;
  # the same ref hidden behind a variable subcommand, then written out literally
  4) bash_ h5 'git $UNSET_SUB show-ref sloprail/checks' ;;
  5) bash_ r1 'git show-ref sloprail/checks' ;;
  6) bash_ r2 'git update-ref refs/sloprail/checks HEAD' ;;
  # a literal subcommand with a substitution only in an argument is decided and permitted
  7) bash_ l1 'git log -1 --format=%s "$(git rev-parse HEAD)"' ;;
  *) finish ;;
esac
