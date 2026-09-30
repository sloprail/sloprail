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
# THE STOP AFTER-CHECK copy: it judges the SETTLED file (PostFileCreate/
# PostFileUpdate). The gate of the same name refuses the pending write first —
# publish is the irreversible step — and this is the backstop for whatever
# landed past it.
#
# REFUSAL CONTRACT: exit 0 permits; non-zero refuses with `{"reason": "..."}` on
# stdout. `set -uo pipefail`, never `set -e`.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset check_publish_lib_loaded
. "$lib_dir/check-publish-lib.sh" || exit 2
[ "${check_publish_lib_loaded:-}" = 1 ] || exit 2
lib_init
# A Post kind carries the SETTLED bytes directly on the flat event.
kind="$(field '.event.kind // ""')" || exit 1
case "$kind" in
  PostFileCreate|PostFileUpdate)
    # A Post kind carries the SETTLED bytes directly on the flat event — when
    # the engine could read them (newContentKnown). Unread: refuse, unchecked.
    [ "$(field '.event.newContentKnown // false')" = "true" ] ||
      refuse "unit-publish-approved: $path could not be read (not a regular file, or too large), so its publish state could not be checked"
    content="$(field '.event.newContent // ""')" || exit 1
    ;;
  *)
    refuse "unit-publish-approved: unexpected event kind '$kind' for $path; this rule only judges settled unit writes"
    ;;
esac
lib_check
