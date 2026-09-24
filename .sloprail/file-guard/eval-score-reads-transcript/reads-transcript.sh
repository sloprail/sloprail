#!/usr/bin/env bash
# Refuses a score.sh that never references SR_EVAL_TRANSCRIPT — a scorer that
# does not read it cannot be judging what the agent-under-test actually did; it
# can only be a fixed verdict, which reports every run a pass (or every run a
# fail) independent of whether the guardrail under test fired at all.
set -uo pipefail

payload="$(cat)"

result_known=$(printf '%s' "$payload" | jq -r '.event.resultKnown')
if [ "$result_known" != "true" ]; then
  echo '{"reason":"the engine could not derive what this write would leave behind, so this cannot be checked before it lands — rerun after the write to get the after-check instead"}'
  exit 1
fi

content=$(printf '%s' "$payload" | jq -r '.event.newContent')

if ! printf '%s' "$content" | grep -q 'SR_EVAL_TRANSCRIPT'; then
  echo '{"reason":"this scorer never references SR_EVAL_TRANSCRIPT, so it cannot be judging what the agent-under-test actually did — it can only be a fixed verdict, which would report every run a pass (or fail) regardless of whether the guardrail under test fired. Read the transcript (sr-session query --whole-session against SR_EVAL_TRANSCRIPT is the documented way) and base the verdict on what it shows."}'
  exit 1
fi

exit 0
