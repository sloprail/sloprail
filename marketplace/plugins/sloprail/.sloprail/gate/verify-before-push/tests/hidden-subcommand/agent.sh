#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }

# Each of these runs a push the engine cannot read as one: the subcommand, or a word in front of it, is a
# variable or a substitution the line never assigned. None may reach the remote.
# the refusal of the literal push names the `sr-checks run --base <b> --head <h>` that judges the range
cmd=$(grep -o 'sr-checks run --base [0-9a-f]* --head [0-9a-f]*' "$A10N_MOCK_SESSION_FILE" 2>/dev/null | tail -1)
case $n in
  0) bash_ h1 'git $UNSET_SUB push -q origin HEAD' ;;
  1) bash_ h2 'git "$(echo push)" -q origin HEAD' ;;
  2) bash_ h3 'timeout $UNSET_T git push -q origin HEAD' ;;
  3) bash_ h4 'env -S "$UNSET_A" git push -q origin HEAD' ;;
  # a cd that is a variable the line never assigned, behind builtin or command, leaves the folder unknown
  4) bash_ h5 'builtin cd "$UNSET_DIR"; git push -q origin HEAD' ;;
  5) bash_ h6 'command cd "$UNSET_DIR"; git push -q origin HEAD' ;;
  # recovery: the literal subcommand is judged like any push, refused until the commits are verified, then permitted
  6) bash_ p1 'git push -q origin HEAD' ;;
  7) if [ -n "$cmd" ]; then bash_ c1 "$cmd"; else finish; fi ;;
  8) bash_ p2 'git push -q origin HEAD' ;;
  *) finish ;;
esac
