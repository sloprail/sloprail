#!/usr/bin/env bash
# exit: reads the paired gate's verdict from `gates`; does not check anything itself.
set -uo pipefail

input="$(cat)"
status="$(printf '%s' "$input" | jq -r '.gates["depth-check"].status // "fail"' 2>/dev/null)"

if [ "$status" = "pass" ]; then
  exit 0
fi

exit 1
