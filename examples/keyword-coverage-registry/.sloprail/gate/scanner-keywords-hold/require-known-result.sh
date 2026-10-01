#!/usr/bin/env bash
# Refuse a create or update whose resulting bytes the engine could not compute.
#
# Every later check reads the pending scanner (event.newContent): the keywords it
# would declare. On a PreFileCreate or PreFileUpdate `resultKnown` is false when
# the engine could not derive them (a `sed -i`, a `python3 -c "open(…).write(…)"`
# it cannot parse, a notebook create), and newContent is then "" — the same
# string as a scanner emptied on purpose. A gate does not fail closed on that by
# itself, so this check does: refuse, and say how to make the write derivable.
# A delete has no result to compute, so it is waved through to the checks that
# read what was lost.
set -uo pipefail

input="$(cat)"
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
[ -n "$kind" ] || { echo "scanner-keywords-hold: could not read the event's kind, so it could not be checked" >&2; exit 2; }
case "$kind" in
  PreFileCreate | PreFileUpdate)
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" != "true" ]; then
      jq -n '{reason: "This write to a scanner cannot be checked: the keywords it would leave in the file cannot be worked out from the command, so a dropped keyword could not be seen. Write the whole scanner.yaml directly (or with sr-file write), citing the user'\''s words with --cite:user if it drops a keyword. Run sr-file alone in its own Bash call, with nothing before or after it on the line: another command beside it (sr-file ...; cat X) is what makes the bytes unknowable, and the edit itself is allowed."}'
      exit 1
    fi
    ;;
esac
exit 0
