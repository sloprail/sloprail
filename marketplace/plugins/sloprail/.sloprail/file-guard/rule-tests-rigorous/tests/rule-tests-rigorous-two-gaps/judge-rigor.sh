#!/usr/bin/env bash
# A mock of the rule-tests-rigorous judge that decides from its input, per RULE. The judge's prompt is an INDEX
# OF PATHS: a "Rule: <name>" line and "- <path>" lines for the rule's files and every case's files, relative to
# the committed snapshot ($SR_TREE). It reads the test.sh of EVERY case listed and judges them TOGETHER: the
# refusal may sit in one case and the permit in another. It lists each gap, numbered, in one line, and it logs one
# line per call (to "$SR_EVENTS_FILE.judges") so the test can count calls.
input="$(cat)"
if ! printf '%s\n' "$input" | grep -q -E '^Rule: demo$'; then
  echo '{"pass":false,"reasoning":"RULE TESTS NOT RIGOROUS: the judge was not given the owning rule demo"}'
  exit 0
fi
case_text=""
n=0
while IFS= read -r p; do
  [ -n "$p" ] || continue
  n=$((n + 1))
  case_text="$case_text$(cat "$SR_TREE/$p")
"
done < <(printf '%s\n' "$input" | sed -n 's/^- \(.*\/tests\/[^/]*\/test\.sh\)$/\1/p')
echo "judged demo cases=$n" >>"$SR_EVENTS_FILE.judges"
gaps=""
k=0
if ! printf '%s\n' "$case_text" | grep -q -E 'outcome *== *"refused"'; then
  k=$((k + 1))
  gaps="$gaps ($k) demo: REFUSAL AND PERMIT: no case asserts a refusal"
fi
if ! printf '%s\n' "$case_text" | grep -q -E 'outcome *== *"(permitted|passed)"'; then
  k=$((k + 1))
  gaps="$gaps ($k) demo: REFUSAL AND PERMIT: no case asserts a permit"
fi
if [ -z "$gaps" ]; then
  echo '{"pass":true,"reasoning":""}'
else
  echo "{\"pass\":false,\"reasoning\":\"RULE TESTS NOT RIGOROUS:$gaps\"}"
fi
