#!/usr/bin/env bash
# A published unit records where it went out, in frontmatter that holds.
#
# The user's approval to publish is not this script's: the guard's `require`
# demands a user citation on a write that moves the unit INTO published (`when:
# ./enters-published.sh`), and the engine refuses an uncited one before this
# runs.
#
# Whenever the change leaves the unit at status: published — entering it or
# already there:
#   valid frontmatter: it must satisfy unit.cue — an invalid one is refused,
#   never read as "no status" and waved through.
#   published_urls:  where it actually went out. A non-empty LIST (a unit may
#   be distributed across several channels); only presence is checked, not
#   each URL's shape (see unit.cue).
# And a unit whose frontmatter cannot be read — YAML that does not parse, a
# status defined twice, a fence after a byte-order mark — is refused: no reader
# can say whether it claims published (publish-claim.sh, shared with
# enters-published.sh, is the one reading of that).
#
# THE GATE copy: it judges the PENDING write (PreFileCreate/PreFileUpdate) —
# publish is the irreversible step, so it is refused before it lands. A result
# the engine could not derive (resultKnown false: sed -i, an unresolvable
# sr-file line) is REFUSED, not waved through: a gate does not fail closed on
# its own. The file-guard of the same name re-checks the settled file (`sr-checks run` judges it, Stop verifies).
#
# REFUSAL CONTRACT: exit 0 permits; non-zero refuses with `{"reason": "..."}` on
# stdout. `set -uo pipefail`, never `set -e`.
lib_dir="$(cd "$(dirname "$0")/../../file-guard/unit-publish-approved" && pwd)"
unset check_publish_lib_loaded
. "$lib_dir/check-publish-lib.sh" || exit 2
[ "${check_publish_lib_loaded:-}" = 1 ] || exit 2
lib_init
# The bytes come from the pending write. resultKnown is consulted before
# newContent is read: newContent is "" when the engine could not compute the
# result, which is indistinguishable from an emptied file.
kind="$(field '.event.kind // ""')" || exit 1
case "$kind" in
  PreFileCreate|PreFileUpdate)
    known="$(field '.event.resultKnown // false')" || exit 1
    if [ "$known" != "true" ]; then
      refuse "unit-publish-approved: the result of this write to $path could not be derived (a shell edit such as sed -i, or an unresolvable sr-file line), so whether it publishes could not be checked. Write the unit's content directly with the Write tool, or use sr-file on its own."
    fi
    content="$(field '.event.newContent // ""')" || exit 1
    ;;
  *)
    refuse "unit-publish-approved: unexpected event kind '$kind' for $path; this gate only judges pending unit writes"
    ;;
esac
lib_check
