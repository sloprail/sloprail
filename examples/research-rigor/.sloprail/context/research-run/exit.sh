#!/usr/bin/env bash
# exit: thin, like goal-tracking (decision slice 7) — does NOT run the depth
# check itself and does NOT refuse the Stop. Reads the paired gate's own
# verdict from `gates`, symmetric to `context`.
set -uo pipefail

input="$(cat)"
status="$(printf '%s' "$input" | jq -r '.gates["depth-check"].status // "fail"' 2>/dev/null)"

if [ "$status" = "pass" ]; then
  exit 0
fi

exit 1
