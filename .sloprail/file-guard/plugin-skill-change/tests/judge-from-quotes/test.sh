#!/usr/bin/env bash
set -euo pipefail
# plugin-skill-change judges a skill's change against the user's words its commits cite (judge
# mocked, deciding from the quotes and the diff it is shown): real words that are not about the
# skill are refused with the judge's reasoning; a follow-up citing the words that ask for it passes.
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
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="plugin-skill-change" and .on=="sr-checks run")] | map(.outcome)==["refused","passed"]' >/dev/null ||
  fail "plugin-skill-change outcomes are not refused, passed"
echo "$RESULT" | jq -e '[.events[]|select(.kind=="FileGuardChecked" and .rule=="plugin-skill-change" and .outcome=="refused")][0].reason | contains("MOCK: the cited words are not about the demo skill")' >/dev/null ||
  fail "the refusal does not carry the judge's reasoning"
