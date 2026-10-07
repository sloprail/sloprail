#!/usr/bin/env bash
# Read each #skip message's line number(s) from the trajectory and log each as
# skip:<transcript>:<line>-<line> into this context's own state — the ref shape
# the gate subtracts via --owner. The agent names the line; transcriptPath
# supplies the path.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath // ""')"

if [ -z "$transcript_path" ]; then
  # No transcript to key skips against — nothing to log, activate quietly.
  jq -n '{skips_declared: "trajectory"}'
  exit 0
fi

# Every #skip message's prose: each entry that wrote a skip tag, its text.
skip_texts="$(sr-session trajectory normalize \
  --path "$transcript_path" \
  --events PostTagWrite \
  | jq -r '
      def msgtext:
        if type == "string" then .
        elif type == "array" then [.[] | select(.type? == "text") | .text] | join(" ")
        elif type == "object" then [(.content // [])[] | select(.type? == "text") | .text] | join(" ")
        else "" end;
      [ .[]
        | select(any(.events[]?;
            .kind == "PostTagWrite" and any(.tags[]?; .label == "skip")))
      ] | .[] | .message | msgtext')"

# Each bare integer in the skip prose is an excused message line; log each as a
# skip:<transcript>:<n>-<n> registry entry.
printf '%s\n' "$skip_texts" | grep -oE '[0-9]+' | sort -u -n | while IFS= read -r n; do
  [ -z "$n" ] && continue
  sr-session state set "skip:${transcript_path}:${n}-${n}" "declared"
done

# Activate. The real payload is the registry just written, read via --owner.
jq -n '{skips_declared: "trajectory"}'
