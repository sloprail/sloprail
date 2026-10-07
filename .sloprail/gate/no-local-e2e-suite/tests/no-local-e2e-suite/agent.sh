#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines. Each turn
# runs one command (ids c1..), the refused ones never run, the permitted ones fail fast (no go.mod).
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }

case $n in
  0) bash_ c1 "go test ./tests/..." ;;
  1) bash_ c2 "go test -run TestA ./tests/e2e/..." ;;
  2) bash_ c3 "go test -run TestA ./tests/e2e/a ./tests/e2e/b ./tests/e2e/c" ;;
  3) bash_ c4 "go test ./tests/e2e/a" ;;
  4) bash_ c5 "make test-e2e" ;;
  5) bash_ c6 "make test-e2e-shard SHARD=session" ;;
  6) bash_ c7 "cd sub && env GOFLAGS=-count=1 go test ./tests/e2e/a" ;;
  7) bash_ c8 "bash -c 'go test ./tests/...'" ;;
  8) bash_ p1 "go test -run TestA ./tests/e2e/a" ;;
  9) bash_ p2 "go test -run=TestA ./tests/e2e/a ./tests/e2e/b" ;;
  10) bash_ p3 "go test ./internal/... ./services/..." ;;
  11) bash_ p4 "env FOO=1 go test -test.run TestA ./tests/e2e/a" ;;
  *) finish ;;
esac
