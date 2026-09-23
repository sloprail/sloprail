#!/usr/bin/env bash
# Extract the declared refactor scope from the trajectory and print it as this
# context's payload. Each scope token is the fqn a landed `sr:moved-from` marker
# will carry — `<path>@<sha>:<start>-<end>` — so the gate can later check for a
# literal match. Printing nothing keeps the prior payload (it does not decline);
# the gate permits on an empty scope, so a turn with no #refactor is harmless.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# The declaration is one message with the #refactor tag and a scope list, e.g.
#   #refactor scope=src/beta.go@<sha>:10-24,src/gamma.go@<sha>:3-9
# Take the last entry carrying the tag and read its text. The tag lives at
# .events[].fields.tags[].label on a normalized entry.
decl="$(sr-session trajectory normalize \
  --path "$transcript_path" \
  --events PostTagWrite \
  | jq -r '
      def msgtext:
        if type == "string" then .
        elif type == "array" then [.[] | select(.type? == "text") | .text] | join("")
        elif type == "object" then [(.content // [])[] | select(.type? == "text") | .text] | join("")
        else "" end;
      [ .[]
        | select(any(.events[]?;
            .kind == "PostTagWrite" and any(.fields.tags[]?; .label == "refactor")))
      ][-1] // {}
      | .message | msgtext')"

if [ -z "$decl" ]; then
  exit 0
fi

scope="$(printf '%s' "$decl" | grep -oE 'scope=[^ ]+' | head -1 | cut -d= -f2)"

jq -n --arg scope "$scope" \
  '{declared_markers: ($scope | split(",")), declared_at: "trajectory"}'
