#!/usr/bin/env bash
# Mock judge for invariant-rigor. Logs the subject ids its prompt names (one line per judge call),
# then decides like the real rubric on one point: a marked test that asserts nothing proves nothing.
# The rubric has the judge read each <test> path itself; the mock reads it from the case project.
input="$(cat)"
printf '%s\n' "$input" | grep -o '<subject id="[^"]*"' | sed 's/.*id="//; s/"$//' | tr '\n' ' ' >> "$JUDGE_LOG"
echo >> "$JUDGE_LOG"
for t in $(printf '%s\n' "$input" | grep -o '<test path="[^"]*"' | sed 's/.*path="//; s/"$//'); do
  # The judge runs in the rule's folder; a <test> path is the project's.
  if ! grep -q 'if ' "$(git rev-parse --show-toplevel)/$t"; then
    echo "{\"pass\": false, \"reasoning\": \"MOCK: $t asserts nothing\"}"
    exit 0
  fi
done
echo '{"pass": true, "reasoning": ""}'
