#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$DOCS/$2" '{file_path:$p}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }
DOCS="$SR_TEST_PLUGINS_DIR/sloprail/skills/authoring-guardrails"

GIT='git -c user.name=t -c user.email=t@t'
G=.sloprail/gate/demo/gate.yaml
P="$(pwd -P)"
V2=$'on:\n  - event: Stop\n'
case $n in
  0) tu k1 Skill '{"skill":"sloprail:authoring-guardrails"}' ;;
  1) read_ r1 gate.md ;;
  2) write_ w1 $G "$V2" ;;
  3) bash_ a1 "git add $G" ;;
  # a -C folder that is a variable the line never assigned cannot be told: refused, write it literally
  4) bash_ u1 "$GIT -C \$UNSET_DIR commit -q -m 'run the demo gate only at Stop'" ;;
  # a folder the line assigns to a literal is that folder: the commit is judged in it, and cited for its own reason
  5) bash_ v1 "D=$P; $GIT -C \$D commit -q -m 'run the demo gate only at Stop'" ;;
  6) bash_ v2 "D=$P && cd \$D && $GIT commit -q -m 'run the demo gate only at Stop'" ;;
  # recovery: the literal form, with the user's words, is decided and permitted
  7) bash_ l1 "$GIT -C $P commit -q -m 'run the demo gate only at Stop' -m 'Sloprail-Cites-User: tighten the demo gate to run only on Stop'" ;;
  *) finish ;;
esac
