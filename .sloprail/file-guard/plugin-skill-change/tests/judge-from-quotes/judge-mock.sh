#!/usr/bin/env bash
# Mock of the plugin-skill-change judge: decides from the quotes it is shown, and refuses when it
# is not shown the skill's diff. The change is asked for when a quote asks to shorten the skill.
in="$(cat)"
printf '%s' "$in" | grep -q '^+A shorter fact.\|^+Short.' || { echo '{"pass":false,"reasoning":"MOCK: no diff of the skill was shown"}'; exit 0; }
if printf '%s' "$in" | grep -o '<quote>[^<]*</quote>' | grep -q 'shorten the demo skill'; then
  echo '{"pass":true,"reasoning":""}'
else
  echo '{"pass":false,"reasoning":"SKILL CHANGE: MOCK: the cited words are not about the demo skill"}'
fi
