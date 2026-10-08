#!/usr/bin/env bash
set -euo pipefail
# plugin-skill-content over a committed range, each refusal beside its permitted neighbour:
# 1. a skill with no brief is refused naming the brief to write, and no judge runs; with the
#    brief written, it passes.
# 2. the judge is handed the whole skill and its brief: a change to one of its two files sends both.
#    A file naming sloprail's source is refused with the judge's reasoning; cut, it passes.
# 3. the brief is part of the verdict: the same skill over the same brief is not judged again,
#    over a different brief it is. (The briefs differ at each range's base: a brief is a rule
#    file, and an edit inside the range would be grounded-rule-changes' business.)
git init -q .
mkdir -p .sloprail/file-guard
cp -R "$SR_TEST_SLOPRAIL_DIR/file-guard/plugin-skill-content" .sloprail/file-guard/
rm -rf .sloprail/file-guard/plugin-skill-content/tests .sloprail/file-guard/plugin-skill-content/briefs
cp -R "$SR_TEST_SLOPRAIL_DIR/_lib" .sloprail/
# the other rules that select skill files are not under test here
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n  - file-guard/skill-quality\n  - file-guard/plugin-skill-length\n  - file-guard/plugin-skill-change\n' > .sloprail/config.yaml
B=.sloprail/file-guard/plugin-skill-content/briefs
mkdir -p "$B"
printf '# demo\n\nDEMO-BRIEF: readers write demo rules.\n' > "$B/demo.md"
commit() { git add -A && git -c user.name=t -c user.email=t@t commit -q -m "$1"; }
commit rules
BASE=$(git rev-parse HEAD)
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")
export JUDGE_LOG="$(mktemp)"
export SR_CHECKS_JUDGE_MOCKS=$(jq -nc --arg p "$SR_TEST_CASE_DIR/judge-mock.sh" '{"file-guard/plugin-skill-content/judge-skill": $p}')

run() { : > "$SR_EVENTS_FILE"; : > "$JUDGE_LOG"; sr-checks run --base "$1" --head HEAD >/dev/null 2>&1; }
dump() { jq -c . "$SR_EVENTS_FILE" >&2; echo "$1" >&2; exit 1; }
refused() { jq -es --arg w "$1" 'any(.[]; .kind=="FileGuardChecked" and .rule=="plugin-skill-content" and .outcome=="refused" and (.reason | contains($w)))' < "$SR_EVENTS_FILE" >/dev/null; }
passed() { jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="plugin-skill-content" and .outcome=="passed") and (any(.[]; .kind=="FileGuardChecked" and .rule=="plugin-skill-content" and .outcome=="refused") | not)' < "$SR_EVENTS_FILE" >/dev/null; }
S=marketplace/plugins/sloprail/skills/demo
skill() { mkdir -p "$S"; printf '# Demo\n\nSee [more](more.md).\n' > "$S/SKILL.md"; printf '# More\n\n%s\n' "$1" > "$S/more.md"; }

# 1. no brief
git checkout -q -b nobrief "$BASE"
mkdir -p marketplace/plugins/sloprail/skills/other
printf '# Other\n' > marketplace/plugins/sloprail/skills/other/SKILL.md
commit other
if run "$BASE"; then dump "1: a skill with no brief passed"; fi
refused "has no brief. Write $B/other.md" || dump "1: no refusal naming $B/other.md"
[ ! -s "$JUDGE_LOG" ] || dump "1: the judge ran on a skill with no brief"
printf '# other\n\nOTHER-BRIEF: readers write other rules.\n' > "$B/other.md"
commit other-brief
run "$BASE" || dump "1: the skill was refused once its brief was written"
passed || dump "1: no passed verdict once the brief exists"

# 2. whole skill, judged
git checkout -q -b judged "$BASE"
skill 'A fact.'
commit skill
run "$BASE" || dump "2: the skill with its brief was refused"
passed || dump "2: no passed verdict for the skill with its brief"
skill 'The engine does this in internal/dispatch/judge.go.'
commit internals
if run "$BASE"; then dump "2: a skill naming sloprail's source passed"; fi
refused "MOCK: names sloprail source internal/dispatch" || dump "2: no refusal carrying the judge's reasoning"
[ "$(cat "$JUDGE_LOG")" = "2 brief=yes" ] || dump "2: the judge was not handed both files and the brief: $(cat "$JUDGE_LOG")"
skill 'The engine does this.'
commit cut
run "$BASE" || dump "2: the cut skill was refused"
passed || dump "2: no passed verdict for the cut skill"

# 3. the brief is in the key: the same skill on a fresh history over the same brief is a stored
#    pass; over a different brief it is judged again.
same_base() { git checkout -q --orphan "$1"; git rm -rq --cached . ; git clean -fdq; git checkout "$BASE" -- .; }
same_base same
commit rules-again
BASE2=$(git rev-parse HEAD)
skill 'A fact.'
commit skill
run "$BASE2" || dump "3: the same skill over the same brief was refused"
[ ! -s "$JUDGE_LOG" ] || dump "3: the same skill over the same brief was judged again"
same_base widened
printf '# demo\n\nDEMO-BRIEF: readers write demo rules and demo gates.\n' > "$B/demo.md"
commit rules-widened
BASE3=$(git rev-parse HEAD)
skill 'A fact.'
commit skill
run "$BASE3" || dump "3: the skill over the widened brief was refused"
[ -s "$JUDGE_LOG" ] || dump "3: a different brief did not re-judge the skill"
