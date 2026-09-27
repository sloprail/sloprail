#!/usr/bin/env bash
# `when` for the user's approval to publish: does this write move the unit INTO
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
# The status is read with the product's own `sr-file validate --emit` against the
# project's unit.cue. A document whose frontmatter does not parse has no status,
# so it is not a publish.
set -uo pipefail

# Undecidable without jq: apply the requirement (exit 0, fail-closed).
command -v jq >/dev/null 2>&1 || exit 0

event="$(cat)"
field() { printf '%s' "$event" | jq -r "$1" 2>/dev/null; }
schema="${SR_WORKSPACE:-.}/.sloprail/schemas/unit.cue"
status_of() {
  printf '%s' "$1" | sr-file validate - --as .md --schema "$schema" --emit 2>/dev/null | jq -r '.status // empty' 2>/dev/null
}

kind="$(field '.event.kind // ""')"
case "$kind" in
  PreFileCreate | PreFileUpdate)
    # A result the engine could not compute is undecidable: apply (exit 0).
    [ "$(field '.event.resultKnown // false')" = "true" ] || exit 0
    ;;
  PostFileCreate | PostFileUpdate) ;;
  *)
    # Deleting a unit publishes nothing.
    exit 1
    ;;
esac

[ "$(status_of "$(field '.event.newContent // ""')")" = "published" ] || exit 1
case "$kind" in
  *Update) [ "$(status_of "$(field '.event.oldContent // ""')")" = "published" ] && exit 1 ;;
esac
exit 0
