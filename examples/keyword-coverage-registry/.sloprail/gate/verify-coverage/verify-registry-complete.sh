#!/usr/bin/env bash
# A separate test from raw depth: was the registry table itself complete,
# and is every entry traceable to a trajectory? The table IS sr-session
# state (decision 20260818_no-slop-primitives, slice 8) — this gate's own
# name (verify-coverage) is its SR_GUARDRAIL, so `sr-session state list`
# reads back every keyword logged during this run by whatever script did
# the logging.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

entries="$(sr-session state list 2>/dev/null)"
count="$(printf '%s' "$entries" | jq 'length' 2>/dev/null || echo 0)"

if [ "$count" -eq 0 ]; then
  echo "No keywords were logged to the registry this run — coverage cannot be verified against an empty table." >&2
  exit 1
fi

# Every entry must trace to a real trajectory position, not a bare keyword
# with no source.
untraced="$(printf '%s' "$entries" | jq -r '[.[] | select(.value | test("^jsonl:") | not)] | length')"

if [ "${untraced:-0}" -gt 0 ]; then
  echo "$untraced registry entries carry no trajectory pointer — a keyword logged with no source is not traceable coverage." >&2
  exit 1
fi

exit 0
