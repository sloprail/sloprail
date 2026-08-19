#!/usr/bin/env bash
# enter: was this branch of the trajectory declared as research? Same
# temporary pattern as #refactor (unit 12) and #eval-loop (unit 14) — a
# grep over the branch's own first message, pending a real tag-query
# structure (decision 20260818_no-slop-primitives, Thread 1, deferred).
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# The FIRST message on this branch — a sub-agent's own opening instruction,
# or the user's turn that started this line of work. Not any #research
# mention anywhere; only the branch's own declared intent counts.
decl="$(sr-session query \
  --transcript "$transcript_path" \
  --select user_message \
  --where 'text contains "#research"' \
  | jq -r '.[0].text // ""')"

if [ -z "$decl" ]; then
  # No research declared on this branch — do not activate.
  exit 0
fi

jq -n '{declared: true}'
