#!/usr/bin/env bash
# exit: is this context done? Thin on purpose (2026-08-19, reversing the
# original build) — this script does NOT run verify itself and does NOT
# refuse the Stop; it only decides whether the context is still active. It
# reads the paired gate's own verdict from `gates`, the map symmetric to
# `context` — the gate ran (or didn't) this same cycle and already decided
# pass/fail; this just reflects that into active/inactive.
set -uo pipefail

input="$(cat)"
status="$(printf '%s' "$input" | jq -r '.event.gates["goal-verify"].status // "fail"' 2>/dev/null)"

if [ "$status" = "pass" ]; then
  # Target met — this context deactivates.
  exit 0
fi

# Not met (or the gate hasn't run yet this cycle) — stay active.
exit 1
