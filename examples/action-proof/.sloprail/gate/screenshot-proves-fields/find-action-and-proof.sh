#!/usr/bin/env bash
# prepare: find, in this turn's trajectory, whether an auditable action (a form
# fill, an invoice download) happened, and the proof artifact that should
# accompany it (a screenshot tool_use output). Hands the judge template the
# facts it needs so the .md.j2 never parses a transcript itself.
#
# Receives GateCheckPayload {event: Stop, transcriptPath}.
#
# Output must nest under additionalContext (2026-08-19, his correction, PR
# #2 review 4974594141: "this prepare script can return an object with a
# key named additionalContext, and everything inside this object will be
# added to the payload under the key additionalContext. So only this key is
# supported.") — the template then reads {{ additionalContext.action }}
# etc, alongside the standard payload fields (event, transcriptPath,
# context), never in place of them.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# The whole trajectory as normalized entries, read once. A tool call is not a
# re-derived event (PreToolUse is deliberately not in the trajectory events) —
# it is just the entry, visible as a tool_use block on .message.content[]. So
# tool-name matching reads raw entries, never --events.
entries="$(sr-session trajectory normalize --path "$transcript_path")"

# Did an auditable action happen this turn? Recognised by the tools that
# perform one — a form submit, a download. (A real deployment would name its
# own; this sample keys on a couple of representative tool names.) The last such
# tool_use block carries the action's .name and .input.
# .content is an ARRAY of blocks on a tool-bearing turn, but a plain text
# message (a typed user prompt, and every session's first turn) carries
# .content as a STRING — iterating that with [] is a jq "Cannot iterate over
# string" fatal. Guard to arrays so a string turn contributes no blocks rather
# than crashing the whole prepare (which fails the check closed before the
# model is ever asked).
action="$(printf '%s' "$entries" | jq -c '
  [ .[] | (.message | objects | .content // [] | if type == "array" then .[] else empty end)
    | select(.type == "tool_use"
        and (.name == "fill_form" or .name == "download_file")) ][-1] // null')"

if [ "$action" = "null" ]; then
  # No auditable action this turn — nothing to demand proof of.
  jq -n '{additionalContext: {action_taken: false}}'
  exit 0
fi

# The proof: the image a screenshot tool produced. The screenshot call is a
# tool_use block (with an id); what it produced lives in the matching entry's
# .toolUseResult (entry.go: "where evidence of what an action actually produced
# lives"), correlated to the call by tool_use id. Pull the most recent one; the
# judge decides whether it actually shows the action's fields.
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
