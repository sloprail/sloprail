#!/usr/bin/env bash
# prepare: find, in this turn's trajectory, whether an auditable action (a form
# fill, an invoice download) happened, and the proof artifact that should
# accompany it (a screenshot tool_use output). Hands the judge template the
# facts it needs so the .md.j2 never parses a transcript itself.
#
# Receives GateCheckPayload {event: Stop, transcriptPath}.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# Did an auditable action happen this turn? Recognised by the tools that
# perform one — a form submit, a download. (A real deployment would name its
# own; this sample keys on a couple of representative tool names.)
action="$(sr-session query \
  --transcript "$transcript_path" \
  --select tool_use \
  --where 'name == "fill_form" or name == "download_file"' \
  | jq -c '.[-1] // null')"

if [ "$action" = "null" ]; then
  # No auditable action this turn — nothing to demand proof of.
  jq -n '{action_taken: false}'
  exit 0
fi

# The proof: a screenshot tool_use whose output is an image. Pull the most
# recent one; the judge decides whether it actually shows the action's fields.
proof="$(sr-session query \
  --transcript "$transcript_path" \
  --select tool_use \
  --where 'name == "screenshot"' \
  | jq -c '.[-1].output // null')"

action_name="$(printf '%s' "$action" | jq -r '.name')"
action_input="$(printf '%s' "$action" | jq -c '.input // {}')"

jq -n \
  --argjson taken true \
  --arg action "$action_name" \
  --argjson action_input "$action_input" \
  --argjson proof "${proof:-null}" \
  '{action_taken: $taken, action: $action, action_input: $action_input, proof: $proof}'
