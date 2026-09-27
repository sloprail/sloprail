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
# And a unit whose frontmatter opens a fence but does not parse is refused: no
# reader can say whether it claims published (publish-claim.sh, shared with
# enters-published.sh, is the one reading of that).
#
# Bound preventive: true in file-guard.yaml — publish is the irreversible
# step — with the Stop after-check as the backstop for a write the engine
# could not derive at Pre (resultKnown false, below).
#
# REFUSAL CONTRACT: exit 0 permits; non-zero refuses with `{"reason": "..."}` on
# stdout. `set -uo pipefail`, never `set -e`.
set -uo pipefail

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

event="$(cat)"
printf '%s' "$event" | jq -e '.event | type == "object"' >/dev/null 2>&1 \
  || refuse "unit-publish-approved: the check payload is not readable JSON with an .event object, so this write could not be checked"

# field FILTER — one jq read of the payload. It runs inside $(…), so on a jq
# error it says why on STDERR (stdout is being captured) and returns non-zero;
# every caller follows it with `|| exit 1`, which refuses with that reason.
field() {
  local out
  if ! out="$(printf '%s' "$event" | jq -r "$1")"; then
    echo "unit-publish-approved: could not read $1 from the check payload, so this write could not be checked" >&2
    return 1
  fi
  printf '%s' "$out"
}

path="$(field '.event.path // ""')" || exit 1
if [ -z "$path" ]; then
  refuse "unit-publish-approved: the event named no path, so there is nothing to check"
fi

root="${SR_WORKSPACE:-.}"
# The schema is the PLUGIN's, read from its own tree — this guard's folder is two
# levels under the plugin's .sloprail/ — never a consumer-side copy.
schema="${SR_GUARDRAIL_DIR:-.}/../../schemas/unit.cue"
if [ ! -f "$schema" ]; then
  refuse "unit-publish-approved: schema not found at $schema — the plugin's own unit.cue is missing, so no unit can be checked."
fi
# shellcheck source=publish-claim.sh
. "${SR_GUARDRAIL_DIR:-.}/publish-claim.sh" 2>/dev/null \
  || refuse "unit-publish-approved: publish-claim.sh is missing beside this check, so whether $path claims published could not be read"

# WHERE THE BYTES COME FROM depends on the kind. resultKnown is consulted on
# BOTH Pre kinds before newContent is read — an underivable result is deferred
# to the Post kind, checked at Stop.
kind="$(field '.event.kind // ""')" || exit 1
case "$kind" in
  PreFileCreate|PreFileUpdate)
    known="$(field '.event.resultKnown // false')" || exit 1
    if [ "$known" != "true" ]; then
      exit 0
    fi
    content="$(field '.event.newContent // ""')" || exit 1
    ;;
  PostFileCreate|PostFileUpdate)
    # A Post kind carries the SETTLED bytes directly on the flat event.
    content="$(field '.event.newContent // ""')" || exit 1
    ;;
  PreFileDelete|PostFileDelete)
    # Deleting a unit is not this rule's business (deletions: skip).
    exit 0
    ;;
  *)
    refuse "unit-publish-approved: unexpected event kind '$kind' for $path; this rule only judges unit writes"
    ;;
esac

publish_claim "$content"
case "$claim" in
  no)
    # Not published: its shape is not this guard's subject.
    exit 0
    ;;
  undecidable)
    refuse "UNREADABLE FRONTMATTER: $path opens a frontmatter fence but it does not parse, so whether it claims status: published cannot be told — fix the frontmatter so it parses:
$claim_why"
    ;;
esac

if ! new_doc="$(printf '%s' "$content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)"; then
  refuse "PUBLISH INVALID: $path claims status: published, but its frontmatter does not satisfy the plugin's unit.cue — fix the frontmatter; a published unit must hold to the schema:
$new_doc"
fi

n_urls="$(printf '%s' "$new_doc" | jq -r '(.published_urls // []) | length')" \
  || refuse "unit-publish-approved: could not read published_urls from $path"
if [ -z "$n_urls" ] || [ "$n_urls" -eq 0 ] 2>/dev/null; then
  refuse "PUBLISH INCOMPLETE: $path claims status: published with no published_urls: in the frontmatter — a unit cannot be published without recording where it went out (a list, e.g. published_urls: [\"https://x.com/you/status/…\"])."
fi

exit 0
