#!/usr/bin/env bash
# A published unit records where it went out.
#
# The user's approval to publish is not this script's: the guard's `require`
# demands a user citation on a write that moves the unit INTO published (`when:
# ./enters-published.sh`), and the engine refuses an uncited one before this
# runs.
#
# Whenever the change leaves the unit at status: published:
#   published_urls:  where it actually went out. A non-empty LIST (a unit may
#   be distributed across several channels); only presence is checked, not
#   each URL's shape (see unit.cue).
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

if ! new_doc="$(printf '%s' "$content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)"; then
  # A document that fails to parse as frontmatter cannot be read for status,
  # so it is not a publish this guard can see — nothing to check rather than
  # a false publish refusal. unit.cue is not close()'d; shape is not this
  # guard's subject.
  exit 0
fi

new_status="$(printf '%s' "$new_doc" | jq -r '.status // empty')" \
  || refuse "unit-publish-approved: could not read the status $path would carry"
if [ "$new_status" != "published" ]; then
  exit 0
fi

n_urls="$(printf '%s' "$new_doc" | jq -r '(.published_urls // []) | length')" \
  || refuse "unit-publish-approved: could not read published_urls from $path"
if [ -z "$n_urls" ] || [ "$n_urls" -eq 0 ] 2>/dev/null; then
  refuse "PUBLISH INCOMPLETE: $path claims status: published with no published_urls: in the frontmatter — a unit cannot be published without recording where it went out (a list, e.g. published_urls: [\"https://x.com/you/status/…\"])."
fi

exit 0
