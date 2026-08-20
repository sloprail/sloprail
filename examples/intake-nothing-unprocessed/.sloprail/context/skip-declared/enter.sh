#!/usr/bin/env bash
# enter: the agent declared a #skip this cycle. Read every #skip message out of
# the trajectory, pull the message line number(s) it named, and log each one as
# skip:<transcript>:<line>-<line> into this context's own state — the exact
# reference shape the intake gate collects for every user message, so the gate
# can subtract them by reading this registry via --owner.
#
# Receives ContextEnterPayload: the PostTagWrite event that fired, the
# transcriptPath, and currentContext. The transcriptPath is what turns a bare
# line number the agent typed into the full /abs/path:line-line reference the
# gate compares against — the agent names the line; the engine, which knows
# which transcript this session is writing, supplies the path.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath // ""')"

if [ -z "$transcript_path" ]; then
  # No record to key skips against — nothing to log. Do not decline loudly; a
  # cycle with no transcript is not the agent's fault, and the gate will simply
  # find nothing excused.
  jq -n '{skips_declared: "trajectory"}'
  exit 0
fi

# Every #skip message's prose. The tag is re-derived as a PostTagWrite event
# (matched by .label == "skip"); the line number(s) it excuses are free text on
# that same entry's assistant message, so take each entry that wrote the skip
# tag and read its text. (Mirrors deterministic-refactoring's enter, which reads
# a declared scope off the #refactor entry's text the same way.)
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
            .kind == "PostTagWrite" and any(.fields.tags[]?; .label == "skip")))
      ] | .[] | .message | msgtext')"

# Each integer that appears after a #skip in the excusing prose is a message
# line the agent marked as needing no task. Pull every bare integer out of the
# skip messages; each becomes a skip:<transcript>:<n>-<n> registry entry.
printf '%s\n' "$skip_texts" | grep -oE '[0-9]+' | sort -u -n | while IFS= read -r n; do
  [ -z "$n" ] && continue
  sr-session state set "skip:${transcript_path}:${n}-${n}" "declared"
done

# Activate, carrying a small marker so a reader can see the context ran. The
# real payload of this context is the registry it just wrote, read via --owner.
jq -n '{skips_declared: "trajectory"}'
