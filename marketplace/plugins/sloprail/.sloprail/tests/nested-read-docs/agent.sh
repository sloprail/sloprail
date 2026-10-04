#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$2" '{file_path:$p}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }

SK="$PWD/.claude/skills/authoring-guardrails"
N=marketplace/plugins/x
case $n in
  0) write_ b1 $N/gate/demo/gate.yaml $'on:\n  - event: Stop\n' ;;
  1) write_ n1 $N/.sloprail/gate/demo/gate.yaml $'on:\n  - event: Stop\n' ;;
  2) write_ n2 $N/.sloprail/context/demo/context.yaml $'on: []\n' ;;
  3) write_ n3 $N/.sloprail/file-guard/demo/file-guard.yaml $'match: "**"\n' ;;
  4) write_ n4 $N/.sloprail/file-guard/structure.yaml $'scope:\n  - glob: "marketplace/plugins/x/"\nallow:\n  - glob: "marketplace/plugins/x/**"\n' ;;
  5) read_ r0 "$SK/SKILL.md" ;;
  6) read_ r1 "$SK/gate.md" ;;
  7) write_ n5 $N/.sloprail/gate/demo/gate.yaml $'on:\n  - event: Stop\n' ;;
  *) finish ;;
esac
