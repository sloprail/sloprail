#!/usr/bin/env bash
# exit: reads the goal-verify gate's verdict from `gates` and reflects pass/fail
# into active/inactive. Does not run verify or refuse the Stop itself.
set -uo pipefail

input="$(cat)"
status="$(printf '%s' "$input" | jq -r '.gates["goal-verify"].status // "fail"' 2>/dev/null)"

if [ "$status" = "pass" ]; then
  # Target met — this context deactivates.
  exit 0
fi

# Not met (or the gate hasn't run yet this cycle) — stay active.
exit 1
