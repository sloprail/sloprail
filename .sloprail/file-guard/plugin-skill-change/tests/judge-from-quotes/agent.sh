#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
# Changes the demo skill citing words that are not about it (judged and refused), then commits a
# follow-up citing the words that ask for it (passes). Each range is judged with `sr-checks run`.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }
RUN='sr-checks run --base $(git rev-list --max-parents=0 HEAD) --head HEAD'
GIT='git -c user.name=t -c user.email=t@t'
F=marketplace/plugins/sloprail/skills/demo/more.md
case $n in
  0) write_ w1 $F $'# More\n\nA shorter fact.\n' ;;
  1) bash_ b1 "git add $F && $GIT commit -q -m 'shorten more.md' -m 'Sloprail-Cites-User: Also tell me the time'" ;;
  2) bash_ c1 "$RUN" ;;
  3) write_ w2 $F $'# More\n\nShort.\n' ;;
  4) bash_ b2 "git add $F && $GIT commit -q -m 'shorten more.md' -m 'Sloprail-Cites-User: shorten the demo skill'" ;;
  5) bash_ c2 "$RUN" ;;
  *) finish ;;
esac
