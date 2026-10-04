#!/usr/bin/env bash
# A mock of the grounded-rule-changes judge: decides from the quotes it is shown. The change is grounded when
# a cited quote asks for it (here: the user asked to tighten the demo gate).
in=$(cat)
if printf '%s' "$in" | grep -o '<quote>[^<]*</quote>' | grep -q 'tighten the demo gate'; then
  echo '{"pass":true,"reasoning":"the user asked to tighten the demo gate"}'
else
  echo '{"pass":false,"reasoning":"the cited words are not about the demo gate"}'
fi
