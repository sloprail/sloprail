#!/usr/bin/env bash
# A unit may not reach `status: published` on its own say-so. It must carry
# BOTH:
#   - a GROUNDED APPROVAL, cited in the unit's BODY (not a frontmatter field
#     — an earlier draft's `approved:` was moved into the body, see unit.cue
#     and the plugin README's migration note): a [quote](jsonl) citation
#     LINK — the SAME shape and the SAME grounding mechanism sloprail-tasks's
#     task body uses for the human's ask (has-body-citation.sh / cite_ground):
#     the quote must resolve, via `sr-session trajectory cite --source-types
#     user`, to a REAL USER MESSAGE. An agent's own prior turn, a tool
#     result, or a harness-injected message (<system-reminder>,
#     <task-notification>, …) does NOT ground — cite excludes all three — so
#     an agent cannot cite its own output as the approval that authorizes
#     itself to publish.
#   - published_urls:  where it actually went out. A non-empty LIST (a unit
#     may be distributed across several channels); this script only checks
#     the list is present and non-empty, not each URL's shape (see unit.cue).
#
# THIS IS THE ONE GUARD IN THE PLUGIN THAT IS PURELY PREVENTIVE-SHAPED: publish
# is the irreversible step (the task explicitly calls out that an agent "must
# not be able to publish on its own say-so"), so it is bound preventive: true
# in file-guard.yaml, refusing the write BEFORE status: published ever lands,
# with a Stop after-check backstop for a write the engine could not derive at
# Pre (see resultKnown handling below, the same shape as every other guard in
# this repo).
#
# It does NOT re-implement citation grounding — it borrows cite_ground and the
# body-link grammar straight from the sloprail-tasks plugin's OWN
# cite-links.sh, vendored beside this guard (see cite-links.sh in this folder,
# copied verbatim with its provenance noted) so the two plugins' "does this
# quote ground to a real user message" logic can never drift against each
# other even though they ship independently.
#
# REFUSAL CONTRACT: exit 0 permits; non-zero refuses with `{"reason": "..."}` on
# stdout. `set -uo pipefail`, never `set -e`.
set -uo pipefail

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  refuse "unit-publish-approved: the event named no path, so there is nothing to check"
fi

root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"

lib="$gdir/cite-links.sh"
if [ ! -f "$lib" ]; then
  refuse "unit-publish-approved: cite-links.sh not found beside this hook at $lib, so no approval citation could be resolved"
fi
# shellcheck source=cite-links.sh
. "$lib"

schema="$root/.sloprail/schemas/unit.cue"
if [ ! -f "$schema" ]; then
  refuse "unit-publish-approved: schema not found at $schema — install the plugin's unit.cue under the project's .sloprail/schemas/."
fi

# WHERE THE BYTES COME FROM depends on the kind. resultKnown is consulted on
# BOTH Pre kinds before newContent is read — an underivable result is deferred
# to the Post kind, checked at Stop.
kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    # A Post kind carries the SETTLED bytes directly on the flat event.
    content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    ;;
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$event" | jq -r '.event.resultKnown // false' 2>/dev/null)"
    if [ "$known" != "true" ]; then
      exit 0
    fi
    content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    ;;
  *)
    exit 0
    ;;
esac

doc="$(printf '%s' "$content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)"
if [ $? -ne 0 ]; then
  # A malformed unit is not this guard's subject (unit.cue's own shape is not
  # even checked elsewhere today, since it is not close()'d) — but a document
  # that fails to parse as YAML frontmatter at all cannot be read for status
  # either, so treat that as nothing-to-check rather than a false publish
  # refusal. A closed-schema violation would be a different guard's job if one
  # is ever added; this guard reads only the fields it needs.
  exit 0
fi

status="$(printf '%s' "$doc" | jq -r '.status // empty' 2>/dev/null)"
if [ "$status" != "published" ]; then
  exit 0
fi

n_urls="$(printf '%s' "$doc" | jq -r '(.published_urls // []) | length' 2>/dev/null)"

# THE BODY IS THE PROSE AFTER THE FRONTMATTER — same extraction every guard
# in this plugin uses. The approval citation lives HERE now, not in
# frontmatter.
body="$(printf '%s\n' "$content" | awk '
  BEGIN { seen = 0 }
  NR == 1 && $0 == "---" { seen = 1; next }
  seen == 1 && $0 == "---" { seen = 2; next }
  seen == 1 { next }
  { print }
')"

problems=""
any_approved=0

# EVERY CITATION LINK IN THE BODY is a candidate approval — the SAME
# extractor task bodies use. At least one must GROUND against the `user`
# pool: a REAL USER MESSAGE approving this unit for publication. An agent
# citing its own prior turn, a tool result, or a harness-injected message is
# refused here, by cite itself: none of those is in the user pool.
while IFS="$(printf '\t')" read -r href quote; do
  [ -n "$href" ] || continue
  cpath="$(cite_link_href_path "$href")"
  case "$cpath" in
    /*) : ;;
    *)  cpath="$root/$cpath" ;;
  esac
  if reason="$(cite_ground user "$cpath" "$quote")"; then
    any_approved=1
  else
    problems="${problems}  approval citation \"$quote\": ${reason}
"
  fi
done <<EOF
$(cite_links_extract "$body")
EOF

if [ "$any_approved" -ne 1 ]; then
  problems="${problems}  no approval citation in the body grounds to a real user message — a unit cannot be published without one
"
fi

if [ -z "$n_urls" ] || [ "$n_urls" -eq 0 ] 2>/dev/null; then
  problems="${problems}  no published_urls: — a unit cannot be published without recording where it went out
"
fi

if [ -n "$problems" ]; then
  IFS= read -r -d '' tail <<'EOF' || true

A unit reaches status: published only with BOTH:

    ## Approval
    The user said: [go ahead, ship it](/abs/session.jsonl:42)

and, in frontmatter:

    published_urls: ["https://x.com/you/status/…"]

The approval is a citation LINK in the BODY (not frontmatter) whose quote
must resolve, via cite, to a REAL USER MESSAGE approving this unit for
publication — not the agent's own prior turn, not a tool result, not a
harness-injected message. An agent cannot publish on its own say-so; ask the
user, quote their answer, and cite it in the body. published_urls: records
where it actually went out, after it does — a list, since a unit may ship on
more than one channel.
EOF
  refuse "PUBLISH NOT APPROVED: $path claims status: published without a valid approval.

$problems
$tail"
fi

exit 0
