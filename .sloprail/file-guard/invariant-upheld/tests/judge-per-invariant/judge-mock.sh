#!/usr/bin/env bash
# Mock judge for invariant-upheld. Logs the invariant its prompt names (one line per judge call),
# then decides like the real rubric on one point: marked code with a bypass branch ("bypass")
# does not uphold the invariant.
input="$(cat)"
printf '%s\n' "$input" | grep -o '<pinned fqn="[^"]*"' | sed 's/.*fqn="//; s/"$//' >> "$JUDGE_LOG"
files="$(printf '%s\n' "$input" | awk '/^<file path=/{on=1} /^<\/file>/{on=0} on')"
if printf '%s\n' "$files" | grep -q 'bypass'; then
  echo '{"pass": false, "reasoning": "MOCK: the bypass branch breaks the invariant"}'
else
  echo '{"pass": true, "reasoning": ""}'
fi
