#!/usr/bin/env bash
# A mock of the rule-tests-rigorous judge that decides from its input: the judge's prompt carries each case
# file inside <case-file> tags and the OWNING rule's files inside a <rule name="..."> tag. It refuses a prompt
# that does not hand it the owner (the plugin p demo gate), and otherwise passes the case only if some case file
# asserts a permitted/passed outcome.
input="$(cat)"
if ! printf '%s\n' "$input" | grep -q -F '<rule name="p/demo">'; then
  echo '{"pass":false,"reasoning":"RULE TEST NOT RIGOROUS: the judge was not given the owning rule p/demo"}'
  exit 0
fi
case_text="$(printf '%s\n' "$input" | awk '/<case-file /{on=1} on{print} /<\/case-file>/{on=0}')"
if printf '%s\n' "$case_text" | grep -q -E 'outcome *== *"(permitted|passed)"'; then
  echo '{"pass":true,"reasoning":""}'
else
  echo '{"pass":false,"reasoning":"RULE TEST NOT RIGOROUS: REFUSAL AND PERMIT: no case file asserts a permitted outcome"}'
fi
