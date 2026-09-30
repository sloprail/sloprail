#!/usr/bin/env bash
# File-guard copy (Stop, settled bytes). `when` for the user's approval to publish: does this write move the unit INTO
# `status: published`? Exit 0 — it does (the status before was anything else, or
# there was no file), so the write must cite the user's approval. Exit 1 — it
# does not (a draft edit, an edit to an already-published unit), so no citation
# is required.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 does not permit anything — it
# APPLIES the requirement. So every path this script cannot decide exits 0, the
# fail-closed direction; only exit 1 waives the citation, and only on a decided
# "not a publish".
#
# Whether the unit claims published is publish-claim.sh's answer, shared with
# check-publish.sh: the frontmatter as written, never read through unit.cue, and
# frontmatter that opens a fence but does not parse is UNDECIDABLE — which here
# applies the requirement, like a publish.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset enters_published_lib_loaded
. "$lib_dir/enters-published-lib.sh" || exit 2
[ "${enters_published_lib_loaded:-}" = 1 ] || exit 2
lib_init
case "$kind" in
  PostFileCreate | PostFileUpdate)
    # Settled bytes the engine could not read — newContentKnown false, a field
    # declared on PostFileCreate/PostFileUpdate (internal/filemod/module.go
    # FieldNewContentKnown): a link to a FIFO or a device, or a file past the
    # read cap. Whether the unit is now published is undecidable: apply.
    [ "$(field '.event.newContentKnown // false')" = "true" ] || exit 0
    ;;
  *)
    # Deleting a unit publishes nothing.
    exit 1
    ;;
esac
new_content="$(field '.event.newContent // ""')"
lib_check
