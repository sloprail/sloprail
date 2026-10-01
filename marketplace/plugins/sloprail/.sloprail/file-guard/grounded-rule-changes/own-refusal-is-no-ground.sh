#!/usr/bin/env bash
# The one grounding that is deterministically no grounding: this rule's own refusal,
# quoted back at it. An agent refused for changing a rule reads the refusal in a tool
# output, and quotes it as the "bug" that justifies changing the rule. A refusal of
# the agent's bad work shows nothing about the rule being wrong.
#
# Runs after `require: citation`, so there is a citation. Refuses when the change
# needs grounding and every citation is either a tool output that carries this
# rule's name (its refusal, echoed) or nothing the user said. A user's own words
# that mention this rule still count, and so does any other tool output.
#
# Contract: a Changeset on stdin. exit 0 permits; refuse with {"reason":...}, exit 1.
set -uo pipefail
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset needs_grounding_lib_loaded
. "$lib_dir/needs-grounding-lib.sh" || exit 2
[ "${needs_grounding_lib_loaded:-}" = 1 ] || exit 2

payload="$(cat)"
refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

[ "$(printf '%s' "$payload" | jq -r '.event.kind // ""')" = "Changeset" ] ||
  refuse "expected a Changeset event, so the changed rules could not be checked"
needing="$(needing_paths "$payload")" ||
  refuse "the changeset's files could not be read, so the changed rules could not be checked"
# A range that only adds rules is not this rule's business.
[ -n "$needing" ] || exit 0

# Citations that ground: the user's words, or a tool output that is not this rule's own refusal.
grounding="$(printf '%s' "$payload" | jq -r '
  [.changeset.citations // [] | .[]
   | select((.sourceTypes | index("user")) != null
            or ((.message // "") | contains("grounded-rule-changes") | not))]
  | length' 2>/dev/null)" ||
  refuse "the changeset's citations could not be read, so the changed rules could not be checked"

if [ "${grounding:-0}" -eq 0 ]; then
  refuse "The only thing cited for this change to the project's rules is this rule's own refusal. A refusal of your work shows the rule doing its job, not misfiring. Ask the user, or cite a tool output that shows the rule refusing CORRECT work, and fix your work instead of the rule. Never disable, loosen or delete a rule to get past it."
fi
exit 0
