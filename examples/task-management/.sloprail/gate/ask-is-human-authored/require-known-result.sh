#!/usr/bin/env bash
# Refuse a write whose resulting bytes the engine could not compute.
#
# The judge reads the pending bytes (event.newContent). On a PreFileCreate or
# PreFileUpdate, `resultKnown` is false when the engine could not derive them
# (a `sed -i`, a notebook create, an unresolvable sr-file line), and newContent
# is then "" — indistinguishable from a write that empties the file. A judge fed
# that would rule on nothing. A gate does not fail closed on this by itself, so
# this check does: refuse, and say how to make the write derivable.
set -uo pipefail

input="$(cat)"
known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
if [ "$known" != "true" ]; then
  jq -n '{reason: "This write to the task ask cannot be checked: the bytes it would leave in the file cannot be worked out from the command. Write the ask with sr-file write, giving the whole content, and cite the user'\''s words with --cite:user. Run sr-file alone in its own Bash call, with nothing before or after it on the line: another command beside it (sr-file ...; cat X) is what makes the bytes unknowable, and the edit itself is allowed."}'
  exit 1
fi
exit 0
