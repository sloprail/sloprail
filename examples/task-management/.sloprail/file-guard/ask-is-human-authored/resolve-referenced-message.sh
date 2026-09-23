#!/usr/bin/env bash
# prepare: resolve the human message ASK.md references out of the session record,
# so the judge template never parses a transcript. Output nests under
# additionalContext — only that key is merged into the payload.
set -uo pipefail

input="$(cat)"
new="$(printf '%s' "$input" | jq -r '.event.newContent')"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# The script already confirmed one of these forms is present; extract it.
ref="$(printf '%s' "$new" | grep -oE '(transcript_path=\S+|message_id=\S+|jsonl:[0-9]+(-[0-9]+)?)' | head -1)"

case "$ref" in
  message_id=*)
    msg_id="${ref#message_id=}"
    # A user message: .type == "user", id in .uuid, text in .message (bare string
    # or a content list of text blocks). No event, so read raw entries.
    message="$(sr-session trajectory normalize \
      --path "$transcript_path" \
      | jq -r --arg id "$msg_id" '
          def msgtext:
            if type == "string" then .
            elif type == "array" then [.[] | select(.type? == "text") | .text] | join("")
            elif type == "object" then
              # .content is EITHER a bare string (a plain typed message) OR a
              # block list. Guard the string case: (.content // [])[] over a
              # string is a jq "Cannot iterate over string" fatal, which would
              # make every message_id reference fail to resolve.
              (.content // "") as $c
              | if ($c | type) == "string" then $c
                else [$c[] | select(.type? == "text") | .text] | join("") end
            else "" end;
          [ .[] | select(.type == "user" and .uuid == $id) ][0] // {}
          | .message | msgtext')"
    ;;
  jsonl:*)
    range="${ref#jsonl:}"
    start="${range%-*}"
    end="${range#*-}"
    [ "$end" = "$range" ] && end="$start"
    message="$(sed -n "${start},${end}p" "$transcript_path" \
      | jq -rs '[.[] | select(.type == "user")][0].message.content // ""')"
    ;;
  *)
    message=""
    ;;
esac

# A reference that does not resolve is reported as such, not silently empty —
# the judge template distinguishes "no message found" from "message is empty".
jq -n --arg m "$message" --arg r "$ref" \
  '{additionalContext: {referenced_message: $m, reference: $r, resolved: ($m != "")}}'
