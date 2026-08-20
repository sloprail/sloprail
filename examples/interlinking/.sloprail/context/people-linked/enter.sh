#!/usr/bin/env bash
# enter: every people/*.md touch this cycle, first or Nth. Logs into the
# registry (sr-session state, keyed on this context's own name) rather than
# trying to grow `payload` in place — state survives independently of which
# firing is "first", so a two-file turn ends up with both entries logged,
# not just the last one's payload overwriting the first's.
set -uo pipefail

input="$(cat)"
path="$(printf '%s' "$input" | jq -r '.event.path // ""')"
kind="$(printf '%s' "$input" | jq -r '.event.kind // ""')"

if [ -z "$path" ]; then
  exit 0
fi

sr-session state set "$path" "$kind"

jq -n '{active_since: "trajectory"}'
