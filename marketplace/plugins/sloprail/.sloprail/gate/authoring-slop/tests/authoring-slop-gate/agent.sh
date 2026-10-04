#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$DOCS/$2" '{file_path:$p}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }
DOCS="$SR_TEST_PLUGINS_DIR/sloprail/skills/authoring-guardrails"

# a hook that reads the written bytes without asking whether the engine could compute them
BAD=$'#!/usr/bin/env bash\nevent="$(cat)"\nprintf \'%s\' "$event" | jq -r \'.event.newContent\' | grep -q TODO && exit 1\nexit 0\n'
# the same hook, asking first
GOOD=$'#!/usr/bin/env bash\nevent="$(cat)"\n[ "$(printf \'%s\' "$event" | jq -r \'.event.resultKnown\')" = true ] || exit 1\nprintf \'%s\' "$event" | jq -r \'.event.newContent\' | grep -q TODO && exit 1\nexit 0\n'
SIMPLE=$'#!/usr/bin/env bash\nexit 0\n'
case $n in
  0) tu s1 Skill '{"skill":"sloprail:authoring-guardrails"}' ;;
  1) read_ r1 script-checks.md ;;
  2) read_ r2 check-template.sh ;;
  3) write_ w1 .sloprail/gate/demo/check.sh "$BAD" ;;
  4) write_ w2 .sloprail/gate/demo/check.sh "$GOOD" ;;
  5) bash_ b1 "printf '#!/usr/bin/env bash\\nexit 0\\n' > .sloprail/gate/demo/other.sh" ;;
  6) write_ w3 .sloprail/gate/demo/other.sh "$SIMPLE" ;;
  7) bash_ b2 "git add .sloprail/gate/demo" ;;
  8) bash_ b3 "git -c user.name=t -c user.email=t@t commit -q -m 'demo gate scripts'" ;;
  *) finish ;;
esac
