#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$DOCS/$2" '{file_path:$p}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }
DOCS="$SR_TEST_PLUGINS_DIR/sloprail/skills/authoring-guardrails"
RUN='sr-checks run --base $(git rev-list --max-parents=0 HEAD) --head HEAD'
GIT='git -c user.name=t -c user.email=t@t'
S=.sloprail/gate/demo/check.sh

# v0 reads newContent without resultKnown. It is written from inside a script, so no gate sees it.
MK=$'mkdir -p .sloprail/gate/demo\ncat > .sloprail/gate/demo/check.sh <<\'SCRIPT\'\n#!/usr/bin/env bash\nevent="$(cat)"\nprintf \'%s\' "$event" | jq -r \'.event.newContent\' | grep -q TODO && exit 1\nexit 0\nSCRIPT\n'
# v2 asks resultKnown, then admits everything: a hook that loads, validates and refuses nothing
INERT=$'#!/usr/bin/env bash\nevent="$(cat)"\n[ "$(printf \'%s\' "$event" | jq -r \'.event.resultKnown\')" = true ] || exit 1\nexit 0\n'
# v3 asks resultKnown and refuses what it is for
GOOD=$'#!/usr/bin/env bash\nevent="$(cat)"\n[ "$(printf \'%s\' "$event" | jq -r \'.event.resultKnown\')" = true ] || exit 1\nprintf \'%s\' "$event" | jq -r \'.event.newContent\' | grep -q TODO && exit 1\nexit 0\n'
case $n in
  0) tu s1 Skill '{"skill":"sloprail:authoring-guardrails"}' ;;
  1) read_ r1 script-checks.md ;;
  2) read_ r2 check-template.sh ;;
  3) write_ w1 mk.sh "$MK" ;;
  4) bash_ s2 "bash mk.sh && rm mk.sh" ;;
  5) bash_ s3 "git add $S" ;;
  6) bash_ b1 "$GIT commit -q -m 'demo gate script'" ;;
  7) bash_ c1 "$RUN" ;;
  8) write_ w2 $S "$INERT" ;;
  9) bash_ s4 "git add $S" ;;
  10) bash_ b2 "$GIT commit -q -m 'ask resultKnown' -m 'Sloprail-Cites-User: add a gate script'" ;;
  11) bash_ c2 "$RUN" ;;
  12) write_ w3 $S "$GOOD" ;;
  13) bash_ s5 "git add $S" ;;
  14) bash_ b3 "$GIT commit -q -m 'refuse what it is for' -m 'Sloprail-Cites-User: add a gate script'" ;;
  15) bash_ c3 "$RUN" ;;
  *) finish ;;
esac
