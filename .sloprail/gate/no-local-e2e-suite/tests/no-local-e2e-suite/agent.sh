#!/usr/bin/env bash
# A scripted agent: claude-mock runs it once per turn; it prints raw stream-json lines. Each turn
# runs one command; the refused ones never run, the permitted ones fail fast (no go.mod).
n=$(grep -c "\"tool_result\"" "$A10N_MOCK_SESSION_FILE" 2>/dev/null); n=${n:-0}
tu() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '{type:"assistant",uuid:("u-"+$id),message:{role:"assistant",stop_reason:null,content:[{type:"tool_use",id:$id,name:$name,input:$input}]}}'; }
bash_() { tu "$1" Bash "$(jq -nc --arg c "$2" '{command:$c}')"; }
finish() { echo '{"type":"result","subtype":"success","result":"done","is_error":false}'; }

cmds=(
  "c1|go test -run Test ./tests/..."
  "c2|go test ./tests/e2e/big"
  "c3|go test -run TestMany ./tests/e2e/many"
  "c4|go test -run '^TestBig' ./tests/e2e/big"
  "c5|make test-e2e"
  "c6|make test-e2e-shard SHARD=session"
  "c7|cd . && env GOFLAGS=-count=1 go test ./tests/e2e/big"
  "c8|bash -c 'go test ./tests/...'"
  "u1|go test -run \"\$X\" ./tests/e2e/a"
  "u2|go test ./tests/e2e/nope"
  "u3|go test -list . ./tests/e2e/a"
  "u4|go test -run '(' ./tests/e2e/a"
  "p1|go test -run 'TestA|TestB' ./tests/e2e/a ./tests/e2e/b"
  "p2|go test -run TestA1 ./tests/e2e/a"
  "p3|go test ./internal/... ./services/..."
  "p4|env FOO=1 go test -test.run=TestB1 ./tests/e2e/b"
  "p5|go test -run 'TestBig01/sub' ./tests/e2e/big"
)
if [ "$n" -lt ${#cmds[@]} ]; then c="${cmds[$n]}"; bash_ "${c%%|*}" "${c#*|}"; else finish; fi
