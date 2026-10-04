#!/usr/bin/env bash
# A mock of the authoring-slop judge: decides from its input. A hook that never refuses anything is the
# slop the rule exists to catch; one that has a refusal path for what it is for passes.
in=$(cat)
if printf '%s' "$in" | grep -q 'grep -q TODO && exit 1'; then
  echo '{"pass":true,"reasoning":"the hook refuses what it is for"}'
else
  echo '{"pass":false,"reasoning":"the hook never refuses anything: it admits every write"}'
fi
