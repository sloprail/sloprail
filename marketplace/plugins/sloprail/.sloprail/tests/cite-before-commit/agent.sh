#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$DOCS/$2" '{file_path:$p}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }
DOCS="$(dirname "$CLAUDE_CODE_PLUGIN_CACHE_DIR")/marketplace/marketplace/plugins/sloprail/skills/authoring-guardrails"

RUN='sr-checks run --base $(git rev-list --max-parents=0 HEAD) --head HEAD'
GIT='git -c user.name=t -c user.email=t@t'
G=.sloprail/gate/demo/gate.yaml
V1=$'on:\n  - event: Stop\n  - event: PreCommandInvoke\n'
V2=$'on:\n  - event: Stop\n'
case $n in
  0) tu k1 Skill '{"skill":"sloprail:authoring-guardrails"}' ;;
  1) read_ r1 gate.md ;;
  # a new file under .sloprail/ weakens nothing: the commit needs no citation
  2) write_ w0 .sloprail/gate/demo/README.md $'A demo gate.\n' ;;
  3) bash_ a0 "git add .sloprail/gate/demo/README.md" ;;
  4) bash_ b0 "$GIT commit -q -m 'document the demo gate'" ;;
  # a change to a rule that already stands needs one: none is refused, with how to cite
  5) write_ w1 $G "$V1" ;;
  6) bash_ a1 "git add $G" ;;
  7) bash_ b1 "$GIT commit -q -m 'tighten the demo gate'" ;;
  # the user's real words, though not about this change: the commit goes through, the judge decides at sr-checks run
  8) bash_ b2 "$GIT commit -q -m 'tighten the demo gate' -m 'Sloprail-Cites-User: Also tell me the time'" ;;
  9) bash_ c1 "$RUN" ;;
  # a follow-up that really changes the file and cites the words that ask for it
  10) write_ w2 $G "$V2" ;;
  11) bash_ a2 "git add $G" ;;
  12) bash_ b3 "$GIT commit -q -m 'run the demo gate only at Stop' -m 'Sloprail-Cites-User: tighten the demo gate to run only on Stop'" ;;
  13) bash_ c2 "$RUN" ;;
  *) finish ;;
esac
