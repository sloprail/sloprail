#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines.
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }

# Each of these runs a commit the engine cannot read as one: the subcommand, or a word in front of it, is a
# variable or a substitution the line never assigned. None may land.
case $n in
  0) bash_ h1 'git $UNSET_SUB commit -q --allow-empty -m x' ;;
  1) bash_ h2 'git "$(echo commit)" -q --allow-empty -m x' ;;
  2) bash_ h3 'timeout $UNSET_T git commit -q --allow-empty -m x' ;;
  3) bash_ h4 'env -S "$UNSET_A" git commit -q --allow-empty -m x' ;;
  # a cd that is a variable the line never assigned, behind builtin or command, leaves the folder unknown
  4) bash_ h5 'builtin cd "$UNSET_DIR"; git commit -q --allow-empty -m x' ;;
  5) bash_ h6 'command cd "$UNSET_DIR"; git commit -q --allow-empty -m x' ;;
  # a global option git(1) does not list may take the next word as its value: the subcommand is in doubt
  6) bash_ h7 'git --future-opt x commit -q --allow-empty -m x' ;;
  # recovery: the literal subcommand changes no guarded file, so it is decided and permitted
  7) bash_ l1 'git -c user.name=t -c user.email=t@t commit -q --allow-empty -m x' ;;
  *) finish ;;
esac
