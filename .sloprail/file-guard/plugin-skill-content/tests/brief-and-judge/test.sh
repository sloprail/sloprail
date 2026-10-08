#!/usr/bin/env bash
set -euo pipefail
# plugin-skill-content over a committed range, each refusal beside its permitted neighbour:
# 1. a skill with no brief is refused naming the brief to write, and no judge runs; with the brief
#    committed, the skill passes.
# 2. the judge is handed the whole skill and its brief: a change to one of its two files sends both.
#    A file naming sloprail's source is refused with the judge's reasoning; cut, it passes.
# 3. the brief is part of the verdict: an unchanged skill is not judged again, an edited brief is.
git init -q .
mkdir -p .sloprail/file-guard
cp -R "$SR_TEST_SLOPRAIL_DIR/file-guard/plugin-skill-content" .sloprail/file-guard/
rm -rf .sloprail/file-guard/plugin-skill-content/tests .sloprail/file-guard/plugin-skill-content/briefs
cp -R "$SR_TEST_SLOPRAIL_DIR/_lib" .sloprail/
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
commit() { git add -A && git -c user.name=t -c user.email=t@t commit -q -m "$1"; }
commit rules
BASE=$(git rev-parse HEAD)
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")
export JUDGE_LOG="$(mktemp)"
export SR_CHECKS_JUDGE_MOCKS=$(jq -nc --arg p "$SR_TEST_CASE_DIR/judge-mock.sh" '{"file-guard/plugin-skill-content/judge-skill": $p}')

# Other project rules (skill-quality) also select SKILL.md; only this rule's verdict is asserted.
run() { : > "$SR_EVENTS_FILE"; : > "$JUDGE_LOG"; sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 || true; }
dump() { jq -c . "$SR_EVENTS_FILE" >&2; echo "$1" >&2; exit 1; }
refused() { jq -es --arg w "$1" 'any(.[]; .kind=="FileGuardChecked" and .rule=="plugin-skill-content" and .outcome=="refused" and (.reason | contains($w)))' < "$SR_EVENTS_FILE" >/dev/null; }
passed() { jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="plugin-skill-content" and .outcome=="passed") and (any(.[]; .kind=="FileGuardChecked" and .rule=="plugin-skill-content" and .outcome=="refused") | not)' < "$SR_EVENTS_FILE" >/dev/null; }S=marketplace/plugins/sloprail/skills/demo
B=.sloprail/file-guard/plugin-skill-content/briefs/demo.md

# 1. no brief
mkdir -p "$S"
printf '# Demo\n\nSee [more](more.md).\n' > "$S/SKILL.md"
printf '# More\n\nA fact.\n' > "$S/more.md"
commit skill
run
refused "has no brief. Write $B" || dump "1: no refusal naming $B"
[ ! -s "$JUDGE_LOG" ] || dump "1: the judge ran on a skill with no brief"
mkdir -p "$(dirname "$B")"
printf '# demo\n\nDEMO-BRIEF: readers write demo rules.\n' > "$B"
commit brief
run
passed || dump "1: no passed verdict once the brief exists"

# 2. whole skill, judged
printf '# More\n\nThe engine does this in internal/dispatch/judge.go.\n' > "$S/more.md"
commit internals
run
refused "MOCK: names sloprail source internal/dispatch" || dump "2: no refusal carrying the judge's reasoning"
[ "$(cat "$JUDGE_LOG")" = "2 brief=yes" ] || dump "2: the judge was not handed both files and the brief: $(cat "$JUDGE_LOG")"
printf '# More\n\nA fact.\n' > "$S/more.md"
commit cut
run
passed || dump "2: no passed verdict for the cut skill"

# 3. cached on the skill and its brief
run
[ ! -s "$JUDGE_LOG" ] || dump "3: the unchanged skill was judged again"
printf '# demo\n\nDEMO-BRIEF: readers write demo rules and demo gates.\n' > "$B"
commit brief-edit
run
[ -s "$JUDGE_LOG" ] || dump "3: an edited brief did not re-judge the skill"
