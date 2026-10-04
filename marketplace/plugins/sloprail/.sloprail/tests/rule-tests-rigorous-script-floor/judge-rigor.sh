#!/usr/bin/env bash
# A mock of the rule-tests-rigorous judge that decides from its input: the judge's prompt carries each case
# file inside <case-file> tags. The case passes only if some case file asserts a permitted/passed outcome.
input="$(cat)"
case_text="$(printf '%s\n' "$input" | awk '/<case-file /{on=1} on{print} /<\/case-file>/{on=0}')"
if printf '%s\n' "$case_text" | grep -q -E 'outcome *== *"(permitted|passed)"'; then
  echo '{"pass":true,"reasoning":""}'
else
  echo '{"pass":false,"reasoning":"RULE TEST NOT RIGOROUS: REFUSAL AND PERMIT: no case file asserts a permitted outcome"}'
fi
