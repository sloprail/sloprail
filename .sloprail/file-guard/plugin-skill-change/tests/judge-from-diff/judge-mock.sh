#!/usr/bin/env bash
# Mock of the plugin-skill-change judge: decides from the diff it is shown, as the rule's second
# criterion would. A diff that removes the long fact and adds it nowhere is a loss; one that keeps
# it passes. Not shown the skill's diff at all, it says so. Every call is counted in $TMPDIR.
in="$(cat)"
echo call >> "${TMPDIR:-/tmp}/plugin-skill-change-judge-calls"
printf '%s' "$in" | grep -q '^-A long fact, said at length.' || { echo '{"pass":false,"reasoning":"MOCK: no diff of the skill was shown"}'; exit 0; }
if printf '%s' "$in" | grep '^+' | grep -q 'A long fact, said at length.'; then
  echo '{"pass":true,"reasoning":""}'
else
  echo '{"pass":false,"reasoning":"SKILL CHANGE: more.md deletes \"A long fact, said at length.\" and no other file of the skill states it (criterion 2, lost); restore it."}'
fi
