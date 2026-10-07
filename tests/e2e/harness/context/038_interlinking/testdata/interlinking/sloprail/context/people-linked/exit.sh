#!/usr/bin/env bash
# exit: thin, reads the paired gate's verdict (verify-linked).
set -uo pipefail

input="$(cat)"
status="$(printf '%s' "$input" | jq -r '.gates["verify-linked"].status // "fail"' 2>/dev/null)"

if [ "$status" = "pass" ]; then
  exit 0
fi

exit 1
