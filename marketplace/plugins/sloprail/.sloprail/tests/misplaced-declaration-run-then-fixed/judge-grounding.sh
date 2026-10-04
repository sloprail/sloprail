#!/usr/bin/env bash
# A mock of the grounded-rule-changes judge: decides from its input. The change is grounded when the
# cited words (the user's) appear in the input next to the changed file.
in=$(cat)
if printf '%s' "$in" | grep -q 'move it where the engine reads it' && printf '%s' "$in" | grep -q '.sloprail/file-guard/structure.yaml'; then
  echo '{"pass":true,"reasoning":"the user asked for the declaration to be moved where the engine reads it"}'
else
  echo '{"pass":false,"reasoning":"the cited words do not ask for this change"}'
fi
