#!/usr/bin/env bash
# File-guard entry. `when` for the user's approval to publish: does this changeset move
# a unit INTO `status: published`? Exit 0 — some unit does (its status at the range's
# base was anything else, or it did not exist), so the range's commits must cite the
# user's approval (a Sloprail-Cites-User: trailer). Exit 1 — none does (a draft edit,
# an edit to an already-published unit), so no citation is required.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 does not permit anything — it
# APPLIES the requirement. So every path this script cannot decide exits 0, the
# fail-closed direction; only exit 1 waives the citation, and only on a decided
# "not a publish".
#
# Whether the unit claims published is publish-claim.sh's answer, shared with
# check-publish.sh: the frontmatter as written, never read through unit.cue, and
# frontmatter that opens a fence but does not parse is UNDECIDABLE — which here
# applies the requirement, like a publish. The comparison itself is the library's.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset enters_published_lib_loaded
. "$lib_dir/enters-published-lib.sh" || exit 2
[ "${enters_published_lib_loaded:-}" = 1 ] || exit 2
lib_setup

payload="$(cat)"
[ "$(printf '%s' "$payload" | jq -r '.event.kind // ""' 2>/dev/null)" = "Changeset" ] || exit 0
n="$(printf '%s' "$payload" | jq -r '.changeset.files | length' 2>/dev/null)" || exit 0
case "$n" in '' | *[!0-9]*) exit 0 ;; esac

# Every file is asked. One that enters published applies the requirement (lib_check
# exits 0 with its hint); only when every file is a decided "not a publish" is the
# citation waived.
i=0
while [ "$i" -lt "$n" ]; do
  idx="$i"
  i=$((i + 1))
  f() { printf '%s' "$payload" | jq -r --argjson i "$idx" ".changeset.files[\$i]$1" 2>/dev/null; }
  status="$(f '.status')" || exit 0
  path="$(f '.path')" || exit 0

  # Deleting a unit publishes nothing.
  [ "$status" = "D" ] && continue

  new_content="$(f '.newContent // ""')" || exit 0
  if [ "$status" = "A" ]; then
    kind=Create old_content=""
  else
    kind=Update
    old_content="$(f '.oldContent // ""')" || exit 0
  fi
  lib_check
done
exit 1
