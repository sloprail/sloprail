#!/usr/bin/env bash
# prepare: find whether an auditable action (form fill, invoice download)
# happened this turn and the screenshot that should prove it, so the judge
# template never parses a transcript. Output nests under additionalContext —
# only that key is merged into the payload.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# Whole trajectory as normalized entries. Tool calls are entries with a tool_use
# block on .message.content[], so match raw entries, not --events.
entries="$(sr-session trajectory normalize --path "$transcript_path")"

# The last auditable action this turn, keyed on representative tool names (a real
# deployment names its own). Guard .content to arrays: a text message carries it
# as a STRING, and iterating that with [] is a jq fatal that fails closed.
action="$(printf '%s' "$entries" | jq -c '
  [ .[] | (.message | objects | .content // [] | if type == "array" then .[] else empty end)
    | select(.type == "tool_use"
        and (.name == "fill_form" or .name == "download_file")) ][-1] // null')"

if [ "$action" = "null" ]; then
  # No auditable action this turn — nothing to demand proof of.
  jq -n '{additionalContext: {action_taken: false}}'
  exit 0
fi

# The proof: the most recent screenshot's output, correlated from the screenshot
# tool_use id to the matching entry's .toolUseResult. The judge decides if it
# actually shows the action's fields.
proof="$(printf '%s' "$entries" | jq -c '
  ([ .[] | (.message | objects | .content // [] | if type == "array" then .[] else empty end)
     | select(.type == "tool_use" and .name == "screenshot") | .id ][-1]) as $sid
  | if $sid == null then null
    else ([ .[]
             | select(any((.message | objects | .content // [] | if type == "array" then .[] else empty end);
                 .type == "tool_result" and .tool_use_id == $sid))
             | .toolUseResult ][-1] // null)
    end')"

action_name="$(printf '%s' "$action" | jq -r '.name')"
action_input="$(printf '%s' "$action" | jq -c '.input // {}')"

jq -n \
  --argjson taken true \
  --arg action "$action_name" \
  --argjson action_input "$action_input" \
  --argjson proof "${proof:-null}" \
  '{additionalContext: {action_taken: $taken, action: $action, action_input: $action_input, proof: $proof}}'
