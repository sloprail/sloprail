#!/bin/sh
# ask-tasked.sh <transcript.jsonl> <prompt.md> <project> < normalized-entries.json
#
# Is the user's GENUINE ask tasked? Reads `sr-session trajectory normalize` output
# of the run on stdin and prints one JSON object: {line, tasked, skipped}.
#   line     the 1-based transcript line of the user entry that is the prompt
#            (null when none is found). The gate counts every user entry —
#            tool results and its own feedback too — so the prompt's own line
#            has to be told apart; it is the entry whose text starts with the
#            prompt's own first words. Text is read from a string content or
#            from an array's text blocks (a tool_result block carries none), and
#            compared with whitespace collapsed and leading whitespace dropped, so
#            neither a content array nor a leading newline hides it.
#   tasked   a tasks/*.md file in the project references that line, as the gate
#            reads one: `(<transcript path>:<line>-<line>)`.
#   skipped  a `#skip <line>` was written for it. Informational: skipping the ask
#            satisfies the gate but is not tasking it.
set -eu
transcript="$1"
prompt="$2"
project="$3"

# The prompt's first words, whitespace collapsed, at most 40 characters.
head="$(tr -s '[:space:]' ' ' < "$prompt" | sed 's/^ //' | cut -c1-40)"

line="$(jq -r --arg head "$head" '
  def text: if type == "string" then .
            elif type == "array" then [.[] | select(type == "object" and .type == "text") | .text] | join(" ")
            else "" end;
  [ .[] | select(.type == "user" and ((.isMeta // false) | not))
    | select((.message.content | text | gsub("\\s+"; " ") | sub("^ "; "")) | startswith($head))
    | .line ] | first // empty')"

tasked="false"
skipped="false"
if [ -n "$line" ]; then
  if grep -rqE ":$line-$line\\)" "$project/tasks" 2>/dev/null; then
    tasked="true"
  fi
  if grep -qE "#skip +$line([^0-9]|\$)" "$transcript" 2>/dev/null; then
    skipped="true"
  fi
fi
jq -n --arg line "$line" --argjson tasked "$tasked" --argjson skipped "$skipped" \
  '{line: (if $line == "" then null else ($line | tonumber) end), tasked: $tasked, skipped: $skipped}'
