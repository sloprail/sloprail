#!/usr/bin/env bash
# Turn two: the user answered "lgtm"; the agent makes a change and cites that reply. CHANGE=proposed (the default)
# makes the change the assistant had proposed; anything else makes an unrelated one.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$DOCS/$2" '{file_path:$p}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }
DOCS="$SR_TEST_PLUGINS_DIR/sloprail/skills/authoring-guardrails"
RUN='sr-checks run --base $(git rev-list --max-parents=0 HEAD) --head HEAD'
GIT='git -c user.name=t -c user.email=t@t'
G=.sloprail/gate/demo/gate.yaml
case "${CHANGE:-proposed}" in
  proposed) NEW=$'on:\n  - event: Stop\n' ;;
  *) NEW=$'on:\n  - event: PreCommandInvoke\n' ;;
esac
case $n in
  0) tu k1 Skill '{"skill":"sloprail:authoring-guardrails"}' ;;
  1) read_ r1 gate.md ;;
  2) write_ w1 $G "$NEW" ;;
  3) bash_ a1 "git add $G" ;;
  4) bash_ b1 "$GIT commit -q -m 'change the demo gate' -m 'Sloprail-Cites-User: lgtm'" ;;
  5) bash_ c1 "$RUN" ;;
  *) finish ;;
esac
