#!/usr/bin/env bash
set -euo pipefail

# A commit whose -C folder is a variable the line never assigned is refused (nothing is judged
# against the hook's cwd instead), telling the agent to write the folder literally. A folder the line assigns to
# a literal (D=<dir>; git -C $D commit, or cd $D) is resolved: the commit is judged in it and refused for its
# own reason, the missing citation. Written literally with the user's words, the commit is permitted.
git init -q .
mkdir -p .sloprail/gate/demo
printf 'on:\n  - event: Stop\n  - event: PreFileWrite\n    match: "true"\n' > .sloprail/gate/demo/gate.yaml
git add .sloprail/gate/demo/gate.yaml
git -c user.name=t -c user.email=t@t commit -q -m "a gate that already stands"
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "tighten the demo gate to run only on Stop. Also tell me the time.")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/cite-before-commit")|{outcome,tool_use_id,reason:(.reason//""|.[0:300])}' >&2; exit 1; }
has() { case "$1" in *"$2"*) return 0 ;; *) return 1 ;; esac; }
verdict() { echo "$RESULT" | jq -e --arg id "$1" --arg out "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/cite-before-commit" and .tool_use_id==$id)] | length==1 and .[0].outcome==$out' >/dev/null; }
reason() { echo "$RESULT" | jq -r --arg id "$1" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/cite-before-commit" and .tool_use_id==$id)][0].reason'; }

verdict u1 refused || fail "u1: a commit through an unresolvable -C folder was not refused"
has "$(reason u1)" "could not check" || fail "u1: the refusal does not say the commit could not be checked"
has "$(reason u1)" 'git -C <literal dir> commit' || fail "u1: the refusal does not tell the agent to write the folder literally"
for id in v1 v2; do
  verdict $id refused || fail "$id: the uncited commit through D=<literal> was not refused"
  has "$(reason $id)" "Sloprail-Cites-User" || fail "$id: the refusal does not name the citation trailer"
  has "$(reason $id)" ".sloprail/gate/demo/gate.yaml" || fail "$id: the refusal does not name the file (the variable folder was not resolved)"
  case "$(reason $id)" in *"could not check"*) fail "$id: the variable assigned to a literal was treated as unresolvable" ;; esac
done
verdict l1 permitted || fail "l1: the literal commit with the user's quote was refused"
# only the cited commit landed
test "$(git log --format=%s | grep -c 'run the demo gate only at Stop$')" = 1 || fail "a refused commit landed, or the cited one did not"
