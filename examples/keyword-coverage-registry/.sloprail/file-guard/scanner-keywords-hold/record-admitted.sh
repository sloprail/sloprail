#!/usr/bin/env bash
# STOP-TIME copy (the file-guard): reads settled bytes. A file-guard never sees a
# Pre event, so the Pre-only `resultKnown` field does not apply here (the gate of
# the same name keeps the pre-write copy and consults it).
# The LAST check: reached only once everything before it admitted the event —
# the citation requirement (when drops-keywords.sh applied it) and the judge.
# It records what the user asked for, so the registry stops owing it:
#
#   - a DELETE of the scanner retires it: `retired:<folder>` = the declaration's
#     current stamp. Without this, a scanner declared this session stayed owed
#     forever after the user had it removed, and every later Stop was refused.
#   - a write that DROPS keywords narrows it: `narrowed:<folder>` =
#     {stamp, keywords: what the scanner now declares}. The registry itself
#     only ever grows (scanner-declared's enter.sh); this is the one way a
#     keyword stops being owed.
#
# Only with a resolved citation of the user's own words on the event. That is
# what "the user asked for it" means here, and it is checked again rather than
# inferred from having been reached: drops-keywords.sh waives the citation for a
# change it decides drops nothing, and a scanner emptied by a write the engine
# could not parse used to reach this point uncited and be retired — the
# obligation gone with the user never asked.
#
# Both records count only while their stamp is the current declaration's
# (scanner-lib.sh), so declaring the scanner again makes it owed in full again,
# and a retirement also needs the file really gone. A delete no rule saw
# (`find … -delete`) never reaches this script and retires nothing.
#
# Nothing to record: permit. A record that cannot be written or read: refuse —
# the record is what makes an admitted change safe to let through, and one that
# silently failed would leave the next Stop refused over what the user asked for.
set -uo pipefail

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

[ -n "${SR_GUARDRAIL_DIR:-}" ] || refuse "The change could not be recorded against the scanner registry (SR_GUARDRAIL_DIR is not set), so it was not let through."
lib="$SR_GUARDRAIL_DIR/../../context/scanner-declared/scanner-lib.sh"
# A helper stopped early runs only partly (whether the `.` then fails depends
# on the bash version); only its last-line sentinel proves it loaded whole.
unset scanner_lib_loaded
# shellcheck source=../../context/scanner-declared/scanner-lib.sh
. "$lib" 2>/dev/null || refuse "The change could not be recorded against the scanner registry (scanner-lib.sh beside scanner-declared could not be loaded), so it was not let through."
[ "${scanner_lib_loaded:-}" = 1 ] || refuse "The change could not be recorded against the scanner registry (scanner-lib.sh did not load whole: its last-line sentinel scanner_lib_loaded is unset), so it was not let through."

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

# Not asked for in the user's own words: record nothing.
has_user_citation "$payload" || exit 0

path="$(field '.event.path // ""')"
[ -n "$path" ] || refuse "The change named no path, so its scanner could not be recorded; nothing was changed."
scanner="$(scanner_dir "$path")"

stamp="$(sr-session state list --owner scanner-declared "stamp:${scanner}" \
  | jq -r -s --arg k "stamp:${scanner}" '[.[] | select(.key == $k) | .value][0] // ""')" \
  || refuse "Could not read scanner-declared's registry to record the change to ${scanner}, so it was not let through. Retry it; if it keeps failing, the sloprail install is broken."

# Never declared this session: nothing is owed, so there is nothing to record.
[ -n "$stamp" ] || exit 0

kind="$(field '.event.kind // ""')"
case "$kind" in
  PostFileDelete)
    sr-session state set "retired:${scanner}" "$stamp" \
      || refuse "Could not record ${scanner} as retired (sr-session state set failed), so the delete was not let through."
    exit 0
    ;;
  PostFileCreate | PostFileUpdate)
    # Settled bytes the engine could not read (newContentKnown false): what
    # the scanner now declares is unknown, so no narrowing is recorded.
    [ "$(field 'if (.event | has("newContentKnown")) then .event.newContentKnown else true end')" = "true" ] || exit 0
    ;;
  *) exit 0 ;;
esac

new="$(scanner_keywords "$(field '.event.newContent // ""')" | jq -R -s -c 'split("\n") | map(select(length > 0))')"
owed="$(registry_keywords "$scanner")" \
  || refuse "Could not read the scanner registry to record the change to ${scanner}, so it was not let through."

# Something owed that the scanner no longer declares: the admitted drop.
if printf '%s' "$owed" | jq -e --argjson new "$new" 'any(.[]; . as $k | ($new | index($k)) == null)' >/dev/null 2>&1; then
  record="$(jq -n -c --arg s "$stamp" --argjson k "$new" '{stamp: $s, keywords: $k}')"
  sr-session state set "narrowed:${scanner}" "$record" \
    || refuse "Could not record the dropped keywords of ${scanner} (sr-session state set failed), so the change was not let through."
fi
exit 0
