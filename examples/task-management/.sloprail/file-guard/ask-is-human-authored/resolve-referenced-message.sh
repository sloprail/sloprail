#!/usr/bin/env bash
# prepare: pull the specific human message ASK.md's reference names, out of
# the session record, so the judge template never has to parse a transcript
# itself. Receives the same CheckPayload the script above did.
#
# Output must nest under additionalContext — only that one key is read from
# prepare's stdout, merged alongside the standard payload, never in place of
# it.
set -uo pipefail

input="$(cat)"
new="$(printf '%s' "$input" | jq -r '.event.newContent')"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# The script already confirmed one of these forms is present; extract it.
ref="$(printf '%s' "$new" | grep -oE '(transcript_path=\S+|message_id=\S+|jsonl:[0-9]+(-[0-9]+)?)' | head -1)"

case "$ref" in
  message_id=*)
    msg_id="${ref#message_id=}"
    # A user message is an entry with .type == "user"; its id is .uuid and its
    # text lives in .message (a bare string, or a content list whose text
    # blocks carry .text). No event is involved, so read raw entries.
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
