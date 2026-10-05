#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$DOCS/$2" '{file_path:$p}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }
DOCS="$SR_TEST_PLUGINS_DIR/sloprail/skills/authoring-guardrails"

# The push is made against ANOTHER repository (OTHER) than the session's own clean project, so a gate that
# judged the hook's cwd, or dropped the variable, would let it through.
OTHER="$(dirname "$(pwd -P)")/other"
# The refusal names the `sr-checks run --base <b> --head <h>` that judges the range: the agent runs it in OTHER, then pushes again.
cmd=$(grep -o 'sr-checks run --base [0-9a-f]* --head [0-9a-f]*' "$A10N_MOCK_SESSION_FILE" 2>/dev/null | tail -1)
case $n in
  # a folder the line never assigned cannot be told: refused, write it literally
  0) bash_ u1 'git -C $UNSET_DIR push -q origin HEAD' ;;
  # a folder the line assigns to a literal is that folder: judged like the literal form
  1) bash_ v1 "D=$OTHER; git -C \$D push -q origin HEAD" ;;
  2) bash_ l1 "git -C $OTHER push -q origin HEAD" ;;
  3) if [ -n "$cmd" ]; then bash_ c1 "cd $OTHER && $cmd"; else finish; fi ;;
  # recovery: the literal form, then the variable form, are decided once the commits are verified
  4) bash_ l2 "git -C $OTHER push -q origin HEAD" ;;
  5) bash_ v2 "D=$OTHER; git -C \$D push -q origin HEAD" ;;
  *) finish ;;
esac
