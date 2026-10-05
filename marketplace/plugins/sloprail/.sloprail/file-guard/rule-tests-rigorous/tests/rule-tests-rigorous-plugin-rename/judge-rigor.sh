#!/usr/bin/env bash
# A mock of the rule-tests-rigorous judge that decides from its input. The judge's prompt is an INDEX OF PATHS:
# a "Rule: <name>" line naming the owning rule and "- <path>" lines for the rule's files and every case's
# files, relative to the committed snapshot ($SR_TREE). It refuses a prompt that does not hand it the owner
# (p/demo or q/demo), reads the test.sh of EVERY case listed, and passes the rule only if the cases TOGETHER assert a
# permitted/passed outcome.
input="$(cat)"
if ! printf '%s\n' "$input" | grep -q -E '^Rule: [pq]/demo$'; then
  echo '{"pass":false,"reasoning":"RULE TESTS NOT RIGOROUS: the judge was not given the owning rule p/demo or q/demo"}'
  exit 0
fi
case_text=""
while IFS= read -r p; do
  [ -n "$p" ] && case_text="$case_text$(cat "$SR_TREE/$p")
"
done < <(printf '%s\n' "$input" | sed -n 's/^- \(.*\/tests\/[^/]*\/test\.sh\)$/\1/p')
if printf '%s\n' "$case_text" | grep -q -E 'outcome *== *"(permitted|passed)"'; then
  echo '{"pass":true,"reasoning":""}'
else
  echo '{"pass":false,"reasoning":"RULE TESTS NOT RIGOROUS: REFUSAL AND PERMIT: no case asserts a permitted outcome"}'
fi
