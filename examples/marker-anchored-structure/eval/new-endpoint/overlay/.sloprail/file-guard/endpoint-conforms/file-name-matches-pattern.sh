#!/usr/bin/env bash
# Settle before the judge: (1) the file name follows the endpoint pattern, and
# (2) log the declared marker into the registry, so a later Stop gate (not built
# here) can total what showed up this session.
set -uo pipefail

input="$(cat)"
path="$(printf '%s' "$input" | jq -r '.event.path')"
name="$(basename "$path")"

# <verb>-<resource>.<ext> — e.g. get-users.ts, create-order.py.
if ! [[ "$name" =~ ^[a-z]+-[a-z0-9-]+\.[a-z]+$ ]]; then
  echo "Endpoint file '$name' does not follow the <verb>-<resource>.<ext> naming pattern (e.g. get-users.ts)." >&2
  exit 1
fi

fqn="$(printf '%s' "$input" | jq -r '(.event.newMarkers // .event.oldMarkers // []) | .[] | select(.kind == "endpoint") | .fqn' | head -1)"
sr-session state set "endpoint:${fqn:-$path}" "$path" >/dev/null 2>&1 || true

exit 0
