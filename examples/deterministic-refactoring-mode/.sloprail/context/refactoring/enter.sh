#!/usr/bin/env bash
# enter: decide whether a refactor has been declared, and if so extract its
# scope. Runs when a PreToolUse event fires (the cheap match already narrowed
# to "some tool is about to run"). Activating means printing the context;
# staying silent (exit 0, no stdout) means "not a refactor, don't activate".
#
# A refactor is declared the way unit 12 describes: the agent states intent
# (#refactor) AND declares the SCOPE as a set of markers, fixed upfront —
# "don't know yet in which files, but they're already set". We read that
# declaration out of the trajectory rather than from a CLI call the agent had
# to remember to make.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# Has the agent declared a refactor this session, and not yet finished one?
# The declaration is a single message carrying the #refactor tag plus the
# marker set it intends to write, e.g.:
#   #refactor scope=sr:moved-from:beta,sr:moved-from:gamma
# The tag is re-derived as a PostTagWrite event (matched by .label, without the
# leading #); the scope= list is free text on that same entry's assistant
# message, so take the LAST entry writing the refactor tag and read its text.
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
            .kind == "PostTagWrite" and any(.tags[]?; .label == "refactor")))
      ][-1] // {}
      | .message | msgtext')"

if [ -z "$decl" ]; then
  # No refactor declared — do not activate.
  exit 0
fi

# Pull the declared marker set out of the declaration. These are the markers
# the agent has committed to writing; the exit check later verifies every one
# actually appeared and its content reconciles.
scope="$(printf '%s' "$decl" | grep -oE 'scope=[^ ]+' | head -1 | cut -d= -f2)"

# Activate: this JSON becomes context[refactoring].payload, readable by any
# guard whose match expression names this context.
jq -n --arg scope "$scope" \
  '{declared_markers: ($scope | split(",")), declared_at: "trajectory"}'
