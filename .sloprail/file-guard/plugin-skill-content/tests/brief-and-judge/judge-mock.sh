#!/usr/bin/env bash
# Mock judge for plugin-skill-content. Logs how many files of the skill it was handed and whether
# the brief reached it, then decides like the real standard on one point: sloprail's own source
# path in a skill is not for its reader.
input="$(cat)"
printf '%s brief=%s\n' "$(printf '%s\n' "$input" | grep -c '<file path=')" \
  "$(printf '%s\n' "$input" | grep -q 'DEMO-BRIEF' && echo yes || echo no)" >> "$JUDGE_LOG"
if printf '%s\n' "$input" | grep -q 'internal/dispatch'; then
  echo '{"pass": false, "reasoning": "SKILL CONTENT: MOCK: names sloprail source internal/dispatch"}'
else
  echo '{"pass": true, "reasoning": ""}'
fi
