#!/usr/bin/env bash
# A mock of the proof judge: the rendered prompt says whether a proof artifact was found.
in=$(cat)
if printf '%s' "$in" | grep -q 'No proof artifact was found'; then
  echo '{"pass":false,"reasoning":"the action has no screenshot proof"}'
elif printf '%s' "$in" | grep -q 'filled contact form'; then
  echo '{"pass":true,"reasoning":""}'
else
  echo '{"pass":false,"reasoning":"the proof does not show the filled fields"}'
fi
