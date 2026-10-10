#!/usr/bin/env bash
set -euo pipefail
# plugin-skill-change judges a skill's change from its diff (judge mocked, deciding from the diff it
# is shown), and no commit has to cite anyone's words: a shortening that loses a fact is refused in
# the rule's own words; the same shortening with the fact kept passes; a file outside any skill is
# not this rule's.
git init -q .
mkdir -p .sloprail/file-guard marketplace/plugins/sloprail/skills/demo
cp -R "$SR_TEST_SLOPRAIL_DIR/file-guard/plugin-skill-change" .sloprail/file-guard/
rm -rf .sloprail/file-guard/plugin-skill-change/tests
cp -R "$SR_TEST_SLOPRAIL_DIR/_lib" .sloprail/
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
printf '# Demo\n\nSee [more](more.md).\n' > marketplace/plugins/sloprail/skills/demo/SKILL.md
printf '# More\n\nA long fact, said at length.\n' > marketplace/plugins/sloprail/skills/demo/more.md
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "rules and a demo skill"
export SR_CHECKS_JUDGE_MOCKS=$(jq -nc --arg p "$SR_TEST_CASE_DIR/judge-mock.sh" '{"file-guard/plugin-skill-change/judge-change": $p}')
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "shorten the demo skill. Also tell me the time.")
fail() { echo "$1" >&2; echo "$RESULT" | jq -c '.events[]|select(.rule=="plugin-skill-change")|{kind,outcome,on,reason}' >&2; exit 1; }
runs() { echo "$RESULT" | jq -c '[.events[]|select(.kind=="FileGuardChecked" and .rule=="plugin-skill-change" and .on=="sr-checks run")]'; }
# the loss is refused, the same shortening with the fact kept passes, and the note outside any skill
# changes neither the verdict nor the number of times the judge was asked
runs | jq -e 'map(.outcome) | .[0:2] == ["refused","passed"] and (.[2:] | all(. == "passed"))' >/dev/null ||
  fail "plugin-skill-change outcomes are not refused, passed (and passed after a change outside the skill)"
[ "$(wc -l < "${TMPDIR:-/tmp}/plugin-skill-change-judge-calls")" = 2 ] ||
  fail "the judge was asked $(wc -l < "${TMPDIR:-/tmp}/plugin-skill-change-judge-calls") times, not twice: a change outside the skill reached it"
# the refusal is the rule's own: its SKILL CHANGE prefix, the file, the lost text and the criterion
runs | jq -e '.[0].reason | startswith("SKILL CHANGE:") and contains("more.md") and contains("A long fact, said at length.") and contains("criterion 2")' >/dev/null ||
  fail "the refusal does not name the file, the lost text and the criterion"
