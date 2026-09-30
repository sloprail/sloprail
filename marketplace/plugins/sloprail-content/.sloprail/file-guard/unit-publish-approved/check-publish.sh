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
# THE FILE-GUARD entry: it judges every UNIT.md the changeset holds, as committed.
# The gate of the same name refuses the pending write first — publish is the
# irreversible step — and this is the backstop for whatever landed past it.
#
# REFUSAL CONTRACT: exit 0 permits; non-zero refuses with `{"reason": "..."}` on
# stdout. `set -uo pipefail`, never `set -e`.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset check_publish_lib_loaded
. "$lib_dir/check-publish-lib.sh" || exit 2
[ "${check_publish_lib_loaded:-}" = 1 ] || exit 2
lib_setup

event="$(cat)"
printf '%s' "$event" | jq -e '.event.kind == "Changeset"' >/dev/null 2>&1 \
  || refuse "unit-publish-approved: the check payload is not a readable Changeset, so these writes could not be checked"
n="$(printf '%s' "$event" | jq -r '.changeset.files | length')" \
  || refuse "unit-publish-approved: could not read the changeset's files, so they could not be checked"
case "$n" in '' | *[!0-9]*) refuse "unit-publish-approved: could not read the changeset's files, so they could not be checked" ;; esac

i=0
while [ "$i" -lt "$n" ]; do
  path="$(printf '%s' "$event" | jq -r --argjson i "$i" '.changeset.files[$i].path')" ||
    refuse "unit-publish-approved: could not read file $i of the changeset, so it could not be checked"
  content="$(printf '%s' "$event" | jq -r --argjson i "$i" '.changeset.files[$i].newContent')" ||
    refuse "unit-publish-approved: could not read $path from the changeset, so it could not be checked"
  i=$((i + 1))
  [ -n "$path" ] || refuse "unit-publish-approved: a file of the changeset named no path, so there is nothing to check"
  lib_check
done
exit 0
