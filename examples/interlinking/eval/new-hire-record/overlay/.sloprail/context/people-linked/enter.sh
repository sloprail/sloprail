#!/usr/bin/env bash
# Fires per people/*.md touch. Logs into state, not `payload`, so a two-file
# turn keeps both entries instead of the last firing overwriting the first.
set -uo pipefail

input="$(cat)"
path="$(printf '%s' "$input" | jq -r '.event.path // ""')"
kind="$(printf '%s' "$input" | jq -r '.event.kind // ""')"

if [ -z "$path" ]; then
  exit 0
fi

sr-session state set "$path" "$kind"

jq -n '{active_since: "trajectory"}'
