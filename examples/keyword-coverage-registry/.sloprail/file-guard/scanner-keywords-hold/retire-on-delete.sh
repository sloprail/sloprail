#!/usr/bin/env bash
# The LAST check: reached only once everything before it admitted the event —
# for a delete, the user's words were cited and the judge found they ask for
# this scanner to go. Such a delete retires the scanner's obligation: without
# this, a scanner declared this session stayed in scanner-declared's registry
# forever, and every later Stop was refused for a search over a scanner the
# user had removed.
#
# It records `retired:<folder>` = the declaration's current stamp (which
# scanner-declared renews at every declaration). registry_owed honours it only
# while that stamp still matches and the file is really gone — so a delete some
# other rule refused, or a scanner declared again later, stays owed. A delete no
# rule saw (`find … -delete`) never reaches this script and retires nothing.
#
# Not a delete: nothing to record, permit. A delete whose stamp cannot be read:
# refuse — the retirement is what makes an admitted delete safe to let through,
# and one that silently failed would leave the next Stop refused over a scanner
# the user asked to remove.
set -uo pipefail

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

kind="$(field '.event.kind // ""')"
case "$kind" in
  PreFileDelete | PostFileDelete) ;;
  *) exit 0 ;;
esac

path="$(field '.event.path // ""')"
if [ -z "$path" ]; then
  jq -n '{reason: "The delete named no path, so its scanner could not be retired from the registry; nothing was deleted."}'
  exit 1
fi
scanner="$(dirname "$path")"

if ! stamps="$(sr-session state list --owner scanner-declared "stamp:${scanner}")"; then
  jq -n --arg s "$scanner" '{reason: ("Could not read scanner-declared'\''s registry to retire " + $s + " (sr-session state list failed), so the delete was not let through. Retry it; if it keeps failing, the sloprail install is broken.")}'
  exit 1
fi
if ! stamp="$(printf '%s' "$stamps" | jq -r -s --arg k "stamp:${scanner}" '[.[] | select(.key == $k) | .value][0] // ""')"; then
  jq -n --arg s "$scanner" '{reason: ("scanner-declared'\''s registry did not parse, so " + $s + " could not be retired and the delete was not let through.")}'
  exit 1
fi

# Never declared this session: there is no obligation to retire.
[ -n "$stamp" ] || exit 0

if ! sr-session state set "retired:${scanner}" "$stamp"; then
  jq -n --arg s "$scanner" '{reason: ("Could not record " + $s + " as retired (sr-session state set failed), so the delete was not let through.")}'
  exit 1
fi
exit 0
