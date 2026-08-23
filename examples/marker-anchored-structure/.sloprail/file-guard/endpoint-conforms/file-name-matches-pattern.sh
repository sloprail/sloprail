#!/usr/bin/env bash
# Two things a script can settle before any judge runs:
#   1. this file's own name follows the endpoint naming pattern
#   2. the per-session declared set (5 endpoints + 1 agent + 1 db-table) is
#      being filled in — logged here so a later completeness check (outside
#      this guard) can read back what showed up this session.
#
# The declared COUNTS themselves (exactly 5 endpoints, not 4 or 6) are not
# this guard's business — a single-file guard re-fires per matched file and
# has no "end of session" moment to total against; that belongs to a gate
# reading this same registry on Stop, the same split used everywhere else
# (unit 20260818_no-slop-primitives, slice 7: this guard's script tracks,
# a gate elsewhere would verify totals — not yet built here, no case demanded
# it before this file-guard's own two checks did).
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
