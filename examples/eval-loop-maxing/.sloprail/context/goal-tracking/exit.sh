#!/usr/bin/env bash
# exit: is this context done? Thin on purpose — does NOT run verify and does
# NOT refuse the Stop; it only decides whether the context is still active. It
# reads the paired gate's verdict from `gates` (the map symmetric to `context`)
# and reflects pass/fail into active/inactive.
set -uo pipefail

input="$(cat)"
status="$(printf '%s' "$input" | jq -r '.gates["goal-verify"].status // "fail"' 2>/dev/null)"

if [ "$status" = "pass" ]; then
  # Target met — this context deactivates.
  exit 0
fi

# Not met (or the gate hasn't run yet this cycle) — stay active.
exit 1
