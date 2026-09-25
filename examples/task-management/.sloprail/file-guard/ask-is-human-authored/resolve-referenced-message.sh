#!/usr/bin/env bash
# prepare: resolve the human message ASK.md references out of the session record,
# so the judge template never parses a transcript. Output nests under
# additionalContext — only that key is merged into the payload.
set -uo pipefail

input="$(cat)"

# resultKnown, defensively — has-message-reference.sh (the script check ahead
# of this prepare) already refuses an underivable Pre write before this ever
# runs, so this should be unreachable with resultKnown false today. Checked
# here too rather than trusted across files: a prepare step is its own
# process with its own payload, and a future change to the script ahead of
# it must not silently make this one read an empty newContent as real
# content.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" != "true" ]; then
      echo "resolve-referenced-message prepare: the engine could not derive this write's content (resultKnown false) — nothing to resolve a reference from." >&2
      exit 1
    fi
    ;;
esac

new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
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
  transcript_path=*)
    # A reference to a DIFFERENT session's transcript, no line range — the
    # first user message in that file (the opening ask), the same "cite a
    # whole file, not a range within it" shape jsonl:N alone already has for
    # a single line. Was previously UNHANDLED: has-message-reference.sh's own
    # regex matches this form and lets it through (README lists it as one of
    # three independent reference forms), but this case statement had no arm
    # for it — every transcript_path= reference silently fell to the `*)`
    # branch below and resolved to "" (message not found), regardless of
    # whether the named transcript, or the message in it, actually existed.
    ref_path="${ref#transcript_path=}"
    if [ -f "$ref_path" ]; then
      message="$(sr-session trajectory normalize --path "$ref_path" \
        | jq -r '[.[] | select(.type == "user")][0].message.content // ""' 2>/dev/null)"
    else
      message=""
    fi
    ;;
  *)
    message=""
    ;;
esac

# A reference that does not resolve is reported as such, not silently empty —
# the judge template distinguishes "no message found" from "message is empty".
jq -n --arg m "$message" --arg r "$ref" \
  '{additionalContext: {referenced_message: $m, reference: $r, resolved: ($m != "")}}'
