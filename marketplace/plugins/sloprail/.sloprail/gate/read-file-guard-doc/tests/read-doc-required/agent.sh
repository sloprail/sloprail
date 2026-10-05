#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
# Loads the skill, writes a file-guard declaration before reading its page (at the root and in a nested .sloprail/), reads
# the page, writes both again, then writes a near-boundary neighbour (.sloprail/file-guard/demo/file-guard.yaml.bak).
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
write_() { tu "$1" Write "$(jq -nc --arg p "$2" --arg c "$3" '{file_path:$p,content:$c}')"; }
read_() { tu "$1" Read "$(jq -nc --arg p "$DOCS/$2" '{file_path:$p}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }
DOCS="$SR_TEST_PLUGINS_DIR/sloprail/skills/authoring-guardrails"
N=marketplace/plugins/x
ROOT=.sloprail/file-guard/demo/file-guard.yaml
NEST=$N/.sloprail/file-guard/demo/file-guard.yaml
CONTENT=$'match: "**"\n'
NCONTENT=$'match: "**"\n'

case $n in
  0) tu s1 Skill '{"skill":"sloprail:authoring-guardrails"}' ;;
  1) write_ ra "$ROOT" "$CONTENT" ;;
  2) write_ na "$NEST" "$NCONTENT" ;;
  3) read_ rr0 file-guard.md ;;
  4) write_ nb "$NEST" "$NCONTENT" ;;
  5) write_ rb "$ROOT" "$CONTENT" ;;
  6) write_ xb .sloprail/file-guard/demo/file-guard.yaml.bak "$CONTENT" ;;
  *) finish ;;
esac
