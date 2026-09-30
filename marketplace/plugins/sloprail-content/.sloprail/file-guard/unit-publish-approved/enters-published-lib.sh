#!/usr/bin/env bash
# Shared by the unit-publish-approved gate and its file-guard: one library, two thin entries.
# The gate entry reads the pending write (lib_init, then a Pre kind); the file-guard
# entry reads the Changeset and calls lib_check once per file. lib_check reads no
# event: the entry sets `kind` (a name ending Create or Update), `path`, `new_content`
# and `old_content`.
#
# lib_check returns 1 when the change is a decided "not a publish" (the requirement is
# waived), and exits 0 with a hint when it applies.

lib_setup() {
  set -uo pipefail

  # Undecidable without jq or the shared reader: apply the requirement (exit 0).
  # DELIBERATE, and fail-closed: in a `when` predicate exit 0 APPLIES the requirement (only exit 1 waives it), so a missing tool or helper applies it rather than permitting.
  command -v jq >/dev/null 2>&1 || exit 0
  # A helper stopped by a syntax error runs only up to it (whether the `.` then
  # fails depends on the bash version): a partial reader may answer "no" with a
  # function it calls missing, so only the last-line sentinel proves it loaded
  # whole. Not loaded whole: apply, never read on and waive.
  unset publish_claim_loaded
  # shellcheck source=publish-claim.sh
  # DELIBERATE, and fail-closed: in a `when` predicate exit 0 APPLIES the requirement (only exit 1 waives it), so a missing tool or helper applies it rather than permitting.
  . "$lib_dir/publish-claim.sh" 2>/dev/null || exit 0
  [ "${publish_claim_loaded:-}" = 1 ] || exit 0
}

# lib_init is the gate's: the pending write's own bytes.
lib_init() {
  lib_setup

  event="$(cat)"
  field() { printf '%s' "$event" | jq -r "$1" 2>/dev/null; }

  kind="$(field '.event.kind // ""')"
}

lib_check() {
  # Only a decided "not published" waives; yes and undecidable both apply.
  publish_claim "$new_content"
  [ "$claim" = "no" ] && return 1
  # The sr-file on PATH cannot read a status at all: apply, and the refusal's
  # hint is the upgrade (check-publish.sh refuses with the same words).
  if [ "$claim" = "unsupported" ]; then
    jq -n --arg why "$claim_why" '{hint: $why}'
    exit 0
  fi

  # Already published before this write: not a transition. Only a DECIDED
  # published counts — an old document no reader can parse is not a published one,
  # and treating it as one would waive the approval.
  from=""
  case "$kind" in
    *Update)
      publish_claim "$old_content"
      [ "$claim" = "yes" ] && return 1
      from="$claim_status"
      ;;
  esac

  # It applies. The hint the refusal carries: only the user publishes, and how —
  # a commit citing their words, or (the gate) an sr-file edit.
  case "$kind" in
    *Create) how="  sr-file write $path --cite:user '<their exact words approving it>' <<'EOF' ... EOF   (status: published, published_urls: [\"<url>\"])" ;;
    *) how="  sr-file edit $path --old-string 'status: ${from:-drafting}' --new-string 'status: published
published_urls: [\"<url>\"]' --cite:user '<their exact words approving it>'" ;;
  esac
  how="$how
  or, in a commit: git commit -m 'Publish <the unit>' -m 'Sloprail-Cites-User: <their exact words approving it>'"
  jq -n --arg how "$how" '{hint: (
    "Only the user publishes: ask them, and once they approve, publish citing their words, with published_urls where it went out:\n" +
    $how + "\nYour own turn, or a tool'\''s output, is not their approval.")}'
  exit 0
}

enters_published_lib_loaded=1
