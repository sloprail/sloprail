#!/usr/bin/env bash
# Mock judge for spec-quality. Logs how many files its bucket hands it to judge (the <file> tags
# between the two headings of the rendered prompt), then decides like the real rubric on one
# point: a judged file naming a harness's own event (PreToolUse) is not harness-neutral.
input="$(cat)"
files="$(printf '%s\n' "$input" | awk '/^## The files to judge/{on=1} /^## Entities they cite/{on=0} on')"
printf '%s\n' "$files" | grep -c '<file path=' >> "$JUDGE_LOG"
if printf '%s\n' "$files" | grep -q 'PreToolUse'; then
  echo '{"pass": false, "reasoning": "MOCK: names the harness event PreToolUse, not harness-neutral"}'
else
  echo '{"pass": true, "reasoning": ""}'
fi
