#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$2" '{file_path:$p}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }

SK="$PWD/.claude/skills/authoring-guardrails"
case $n in
  0) write_ w1 marketplace/plugins/x/.sloprail/structure.yaml $'allow:\n  - glob: "**"\n' ;;
  1) read_ r0 "$SK/SKILL.md" ;;
  2) read_ r1 "$SK/structure-gate.md" ;;
  3) write_ w2 marketplace/plugins/x/.sloprail/file-guard/structure.yaml $'scope:\n  - glob: "marketplace/plugins/x/"\nallow:\n  - glob: "marketplace/plugins/x/**"\n' ;;
  4) write_ w3 marketplace/plugins/x/.sloprail/gate/demo/data.yaml $'note: a data file a check reads\n' ;;
  5) write_ w4 marketplace/plugins/x/structure.yaml $'note: not under a dot folder\n' ;;
  *) finish ;;
esac
