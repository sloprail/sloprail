#!/usr/bin/env bash
set -euo pipefail

# A push whose -C folder is a variable the line assigned to a literal is judged in THAT folder, exactly like the
# literal form (the session's own project is clean, so judging it instead would let the push through); a folder
# the line never assigned is refused with the reason that tells the agent to write it literally; once the
# agent has judged the commits, both the literal and the variable form are permitted.
BASE="$(dirname "$(pwd -P)")"
OTHER="$BASE/other"
REMOTE="$BASE/remote.git"
git init -q --bare "$REMOTE"
# the session's own project: clean, nothing to push
git init -q -b main .
mkdir -p .sloprail
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m init
# the other repository holds the unverified commit on a branch
git init -q -b main "$OTHER"
mkdir -p "$OTHER/.sloprail"
# (the demo gate's script is incidental, not under test: authoring-slop would judge it)
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n  - sloprail/file-guard/authoring-slop\n' > "$OTHER/.sloprail/config.yaml"
# the plugins are enabled through the project's .claude/settings.local.json, which the agent run writes later:
# link OTHER to it, so the rules judge OTHER's commits too
ln -s "$PWD/.claude" "$OTHER/.claude"
printf '.claude\n' >> "$OTHER/.git/info/exclude"
git -C "$OTHER" add -A && git -C "$OTHER" -c user.name=t -c user.email=t@t commit -q -m init
git -C "$OTHER" remote add origin "$REMOTE"
git -C "$OTHER" push -q origin main
git -C "$OTHER" checkout -q -b feature
mkdir -p "$OTHER/.sloprail/gate/demo"
printf 'on:\n  - event: Stop\nchecks:\n  - script: ./ok.sh\n' > "$OTHER/.sloprail/gate/demo/gate.yaml"
printf '#!/usr/bin/env bash\nexit 0\n' > "$OTHER/.sloprail/gate/demo/ok.sh"
chmod +x "$OTHER/.sloprail/gate/demo/ok.sh"
git -C "$OTHER" add .sloprail/gate/demo
git -C "$OTHER" -c user.name=t -c user.email=t@t commit -q -m "add a gate"

RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "push the other repo")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="sloprail/verify-before-push")|{outcome,tool_use_id,reason:(.reason//""|.[0:200])}' >&2; exit 1; }
verdict() { echo "$RESULT" | jq -e --arg id "$1" --arg out "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/verify-before-push" and .tool_use_id==$id)] | length==1 and .[0].outcome==$out' >/dev/null; }
reason() { echo "$RESULT" | jq -r --arg id "$1" '[.events[]|select(.kind=="GateChecked" and .rule=="sloprail/verify-before-push" and .tool_use_id==$id)][0].reason'; }
has() { case "$1" in *"$2"*) return 0 ;; *) return 1 ;; esac; }

verdict u1 refused || fail "u1: a push from an unassigned variable folder was not refused"
has "$(reason u1)" 'git -C <literal dir> push' || fail "u1: the refusal does not tell the agent to write the folder literally"
# the variable form is read as OTHER and refused for the same reason as the literal form
verdict v1 refused || fail "v1: the push through D=<literal> was not refused"
verdict l1 refused || fail "l1: the literal push was not refused"
for id in v1 l1; do
  has "$(reason $id)" "$OTHER" || fail "$id: the refusal does not name the repository $OTHER"
  has "$(reason $id)" 'sr-checks run --base' || fail "$id: the refusal does not name the run that judges the commits"
done
SESSION=$(echo "$RESULT" | jq -er .session)
for id in u1 v1 l1; do
  jq -es --arg id "$id" 'any(.[]; .type=="user" and any(.message.content[]?; .type=="tool_result" and .tool_use_id==$id and .is_error==true))' "$SESSION" >/dev/null || fail "the refused push ($id) has no error tool_result"
done
# recovery: after the run, the literal and the variable form are decided and permitted
verdict l2 permitted || fail "l2: the literal push after the run was refused"
verdict v2 permitted || fail "v2: the variable push after the run was refused"
test "$(git -C "$REMOTE" rev-parse refs/heads/feature)" = "$(git -C "$OTHER" rev-parse HEAD)" || fail "the remote's feature is not OTHER's HEAD"
jq -es 'any(.[]; .type=="user" and any(.message.content[]?; .type=="tool_result" and .tool_use_id=="c1" and (.is_error|not)))' "$SESSION" >/dev/null || fail "sr-checks run (c1) did not succeed"
