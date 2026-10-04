#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$DOCS/$2" '{file_path:$p}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }
DOCS="$(dirname "$CLAUDE_CODE_PLUGIN_CACHE_DIR")/marketplace/marketplace/plugins/sloprail/skills/authoring-guardrails"

# config.yaml's disabled: list switches rules off, so a change to it is a decision: it cites the user.
CFG=$'disabled:\n  - sloprail/gate/no-self-matching-pgrep\n'
case $n in
  0) write_ w1 .sloprail/config.yaml "$CFG" ;;
  1) bash_ s1 "sr-file write .sloprail/config.yaml --cite:user 'turn off the pgrep gate' <<'BODY'
disabled:
  - sloprail/gate/no-self-matching-pgrep
BODY" ;;
  2) write_ w2 .sloprail/gate/demo/README.md $'A demo gate.\n' ;;
  *) finish ;;
esac
