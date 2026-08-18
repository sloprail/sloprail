#!/usr/bin/env bash
# prepare: pull the specific human message ASK.md's reference names, out of
# the session record, so the judge template never has to parse a transcript
# itself. Receives the same CheckPayload the script above did.
set -uo pipefail

input="$(cat)"
new="$(printf '%s' "$input" | jq -r '.event.newContent')"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# The script already confirmed one of these forms is present; extract it.
ref="$(printf '%s' "$new" | grep -oE '(transcript_path=\S+|message_id=\S+|jsonl:[0-9]+(-[0-9]+)?)' | head -1)"

case "$ref" in
  message_id=*)
    msg_id="${ref#message_id=}"
    message="$(sr-session query \
      --transcript "$transcript_path" \
      --select user_message \
      --where "id == \"$msg_id\"" \
      | jq -r '.[0].text // ""')"
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
  '{referenced_message: $m, reference: $r, resolved: ($m != "")}'
