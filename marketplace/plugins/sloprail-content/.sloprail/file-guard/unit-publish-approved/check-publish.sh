#!/usr/bin/env bash
# A unit may not reach `status: published` on its own say-so.
#
# THE APPROVAL RIDES ON THE ACTION, NOT IN THE FILE. A write that TRANSITIONS
# a unit INTO `status: published` (the status before the change is anything
# else, or there was no file) must carry at least one citation whose
# sourceTypes include `user` — the user's own words approving it, e.g.
#
#   sr-file edit <UNIT.md> --old-string 'status: drafting' --new-string 'status: published' --cite:user 'ship it'
#
# The session resolves the quote against its own record before this runs and
# puts it on `.event.citations` only if it is a real entry of that pool. An
# agent's own prior turn, a tool result, or a harness-injected message is not
# in the `user` pool, so an agent cannot cite its own output as the approval
# that authorizes itself to publish. The unit keeps no approval text and no
# transcript link: a `[quote](/abs/session.jsonl:N)` in a repository file
# does not resolve on any other machine.
#
# "Before the change" is `oldContent`: the file on disk at a Pre kind, the
# SESSION BASELINE at a Post kind — and at Post `.event.citations` is every
# citation recorded for the path this session (an uncited change clears
# them), so both moments judge the same span.
#
# ALSO, whenever the change leaves the unit at status: published:
#   published_urls:  where it actually went out. A non-empty LIST (a unit may
#   be distributed across several channels); only presence is checked, not
#   each URL's shape (see unit.cue).
#
# A write that does not move the unit into published (a draft edit, an edit
# to an already-published unit) needs no citation.
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
schema="$root/.sloprail/schemas/unit.cue"
if [ ! -f "$schema" ]; then
  refuse "unit-publish-approved: schema not found at $schema — install the plugin's unit.cue under the project's .sloprail/schemas/."
fi

# WHERE THE BYTES COME FROM depends on the kind. resultKnown is consulted on
# BOTH Pre kinds before newContent is read — an underivable result is deferred
# to the Post kind, checked at Stop. A create has no oldContent: nothing
# preceded it.
kind="$(field '.event.kind // ""')" || exit 1
old_content=""
has_old=false
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
case "$kind" in
  PreFileUpdate|PostFileUpdate)
    old_content="$(field '.event.oldContent // ""')" || exit 1
    has_old=true
    ;;
esac

# status_of CONTENT — the unit's frontmatter status, or nothing when the
# frontmatter cannot be parsed or carries none.
status_of() {
  local doc
  doc="$(printf '%s' "$1" | sr-file validate - --as .md --schema "$schema" --emit 2>/dev/null)" || return 0
  printf '%s' "$doc" | jq -r '.status // empty' 2>/dev/null
}

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

old_status=""
[ "$has_old" = "true" ] && old_status="$(status_of "$old_content")"

problems=""

n_urls="$(printf '%s' "$new_doc" | jq -r '(.published_urls // []) | length')" \
  || refuse "unit-publish-approved: could not read published_urls from $path"
if [ -z "$n_urls" ] || [ "$n_urls" -eq 0 ] 2>/dev/null; then
  problems="${problems}  no published_urls: in the frontmatter — a unit cannot be published without recording where it went out (a list, e.g. published_urls: [\"https://x.com/you/status/…\"])
"
fi

if [ "$old_status" != "published" ]; then
  n_user="$(field '[(.event.citations // [])[] | select((.sourceTypes // []) | index("user"))] | length')" || exit 1
  if [ "$n_user" -eq 0 ] 2>/dev/null; then
    from="${old_status:-drafting}"
    case "$kind" in
      Pre*)
        if [ "$has_old" = "true" ]; then
          how="  sr-file edit $path --old-string 'status: $from' --new-string 'status: published' --cite:user '<exact quote>'"
        else
          how="  sr-file write $path --cite:user '<exact quote>' <<'EOF' ... EOF"
        fi
        problems="${problems}  this change moves $path to status: published and carries no citation of the user's own words approving it. An agent cannot publish on its own say-so: ask the user, and once they approve, make the change with sr-file, quoting their approval verbatim:
$how
  Run sr-file ON ITS OWN in the command (nothing else in the line but sr-file calls, &&, and echo) so its result can be checked before it runs. Single-quote the quote; it must match exactly one user message of this session's record — check one with \`sr-session trajectory cite '<quote>'\`. The approval rides on the command, never in the file: do not paste it or a transcript link into the unit.
"
        ;;
      *)
        problems="${problems}  $path was moved to status: published without a citation of the user's own words approving it. Take it back out of published, then, once the user approves, publish it again with sr-file, quoting their approval verbatim:
  sr-file edit $path --old-string 'status: published' --new-string 'status: $from'
  sr-file edit $path --old-string 'status: $from' --new-string 'status: published' --cite:user '<exact quote>'
  The quote must match exactly one user message of this session's record — check one with \`sr-session trajectory cite '<quote>'\`. The approval rides on the command, never in the file.
"
        ;;
    esac
  fi
fi

if [ -n "$problems" ]; then
  refuse "PUBLISH NOT APPROVED: $path claims status: published without everything publishing needs.

$problems"
fi

exit 0
