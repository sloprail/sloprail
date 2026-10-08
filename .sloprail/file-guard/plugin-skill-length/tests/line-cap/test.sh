#!/usr/bin/env bash
set -euo pipefail
# plugin-skill-length over a committed range: a 201-line skill file is refused naming the file and
# the count; cut to 200 lines, the range passes. A long file outside the skills is not selected.
git init -q .
mkdir -p .sloprail/file-guard
cp -R "$SR_TEST_SLOPRAIL_DIR/file-guard/plugin-skill-length" .sloprail/file-guard/
rm -rf .sloprail/file-guard/plugin-skill-length/tests
# the other rules that select skill files are not under test here
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n  - file-guard/skill-quality\n  - file-guard/plugin-skill-content\n  - file-guard/plugin-skill-change\n' > .sloprail/config.yaml
commit() { git add -A && git -c user.name=t -c user.email=t@t commit -q -m "$1"; }
commit rules
BASE=$(git rev-parse HEAD)
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")

run() { : > "$SR_EVENTS_FILE"; sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1; }
verdict() { jq -es --arg o "$1" '[.[] | select(.kind=="FileGuardChecked" and .rule=="plugin-skill-length")] | length==1 and .[0].outcome==$o' < "$SR_EVENTS_FILE" >/dev/null; }
dump() { jq -c . "$SR_EVENTS_FILE" >&2; echo "$1" >&2; exit 1; }
F=marketplace/plugins/sloprail/skills/demo/SKILL.md

mkdir -p "$(dirname "$F")" docs
seq 201 > "$F"
seq 300 > docs/long.md
commit too-long
if run; then dump "a 201-line skill file passed"; fi
verdict refused || dump "a 201-line skill file was not refused"
jq -es --arg f "$F" 'any(.[]; .kind=="FileGuardChecked" and .rule=="plugin-skill-length" and .outcome=="refused" and (.reason | contains($f + ": 201 lines, over the cap of 200")))' < "$SR_EVENTS_FILE" >/dev/null ||
  dump "no refusal naming $F and its 201 lines"
jq -es 'all(.[]; (.reason // "") | contains("docs/long.md") | not)' < "$SR_EVENTS_FILE" >/dev/null || dump "docs/long.md was judged"

seq 200 > "$F"
commit cut
run || dump "the 200-line skill file was refused"
verdict passed || dump "the 200-line skill file did not pass"
