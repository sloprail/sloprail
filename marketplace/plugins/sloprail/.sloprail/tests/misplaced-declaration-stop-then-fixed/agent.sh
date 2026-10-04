#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
fb=$(grep -c "Stop hook feedback" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); fb=${fb:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }


# The gate sees a write the tool call or the command line names; it cannot see inside a script. The
# agent writes the declaration one level too high from inside a script, so only Stop catches it.
# Stop says "not judged yet" and names the `sr-checks run --base .. --head ..` to run: the agent runs it.
MK=$'mkdir -p .sloprail\nprintf \'allow:\\n  - glob: ".sloprail/**"\\n\' > .sloprail/structure.yaml\n'
cmd=$(grep -o 'sr-checks run --base [0-9a-f]* --head [0-9a-f]*' "$A10N_MOCK_SESSION_FILE" 2>/dev/null | tail -1)
ran=0; [ -n "$cmd" ] && ran=$(grep -c "\"command\":\"$cmd\"" "$A10N_MOCK_SESSION_FILE")
case $n in
  0) write_ w1 mk.sh "$MK" ;;
  1) bash_ s1 "bash mk.sh && rm mk.sh" ;;
  2) bash_ s2 "git add .sloprail/structure.yaml" ;;
  3) bash_ b1 "git -c user.name=t -c user.email=t@t commit -q -m structure" ;;
  4) if [ -n "$cmd" ]; then bash_ c1 "$cmd"; else finish; fi ;;
  5) bash_ b2 "mkdir -p .sloprail/file-guard && git mv .sloprail/structure.yaml .sloprail/file-guard/structure.yaml" ;;
  6) bash_ b3 "git -c user.name=t -c user.email=t@t commit -q -m move-structure" ;;
  7) if [ -n "$cmd" ] && [ "$ran" -eq 0 ]; then bash_ c2 "$cmd"; else finish; fi ;;
  *) finish ;;
esac
