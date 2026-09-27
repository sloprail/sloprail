#!/usr/bin/env bash
# prepare for stage 2 of content-rule-is-grounded: hand the judge the user's
# own words this change was grounded in, so judge-rule-body.md.j2 never has to
# find them itself.
#
# THE GROUND TRUTH IS `.event.citations`, not the rule's body. The agent made
# the change with `sr-file write|edit ... --cite:user '<exact quote>'`, and the
# session already resolved each quote against its own record before this ran:
# every entry is `{quote, sourceTypes, path, line}` naming a real entry of the
# transcript. Only entries whose sourceTypes include `user` are ground truth
# here — the guard's require is the user pool, and a tool's output is not the
# human's ask. At a Pre kind they are this change's citations; at a Post kind
# (Stop) they are every citation recorded for the path this session.
#
# The body is handed over too, and on an UPDATE so is the body BEFORE the
# change (oldContent: the file on disk at Pre, the session baseline at Post —
# the same span the citations cover), so the judge weighs what the change
# added or altered rather than holding text carried over unchanged to a quote
# that was never about it.
#
# THE PREPARE CONTRACT: exit 0 with additionalContext proceeds to the judge;
# non-zero fails the check closed. Every read of the payload goes through
# field(), which fails the prepare (and so the check) on an unreadable payload
# rather than handing the judge an empty ground truth as if none were cited.
set -uo pipefail

fail() {
  echo "content-rule-is-grounded: $1" >&2
  exit 1
}

event="$(cat)"
printf '%s' "$event" | jq -e '.event | type == "object"' >/dev/null 2>&1 \
  || fail "the check payload is not readable JSON with an .event object, so the cited words could not be assembled"

# field FILTER — one jq read of the payload; a jq error fails the prepare.
field() {
  local out
  out="$(printf '%s' "$event" | jq -r "$1")" || fail "could not read $1 from the check payload"
  printf '%s' "$out"
}

# body_of CONTENT — the prose after the frontmatter, the same extraction every
# guard in this plugin uses.
body_of() {
  printf '%s\n' "$1" | awk '
    BEGIN { seen = 0 }
    NR == 1 && $0 == "---" { seen = 1; next }
    seen == 1 && $0 == "---" { seen = 2; next }
    seen == 1 { next }
    { print }
  '
}

kind="$(field '.event.kind // ""')" || exit 1
content=""
old_content=""
case "$kind" in
  PostFileCreate)
    content="$(field '.event.newContent // ""')" || exit 1
    ;;
  PostFileUpdate)
    content="$(field '.event.newContent // ""')" || exit 1
    old_content="$(field '.event.oldContent // ""')" || exit 1
    ;;
  PreFileCreate|PreFileUpdate)
    # newContent is only meaningful alongside resultKnown; an unknown result
    # leaves the body empty (the engine already fails a preventive guard
    # closed on an underivable pre-write, so the judge never sees one).
    known="$(field '.event.resultKnown // false')" || exit 1
    if [ "$known" = "true" ]; then
      content="$(field '.event.newContent // ""')" || exit 1
    fi
    if [ "$kind" = "PreFileUpdate" ]; then
      old_content="$(field '.event.oldContent // ""')" || exit 1
    fi
    ;;
  *)
    fail "unexpected event kind '$kind'; this guard judges only rule creates and updates"
    ;;
esac

body="$(body_of "$content")"
old_body=""
[ -n "$old_content" ] && old_body="$(body_of "$old_content")"

cited_messages="$(field '
  [ (.event.citations // [])[]
    | select((.sourceTypes // []) | index("user"))
    | "--- the user said (transcript \(.path), line \(.line)):\n\(.quote)\n" ]
  | join("\n")')" || exit 1

cited_ok=false
[ -n "$cited_messages" ] && cited_ok=true

jq -n --arg msgs "$cited_messages" --argjson ok "$cited_ok" \
  --arg body "$body" --arg old_body "$old_body" \
  '{additionalContext: {cited_messages: $msgs, cited_ok: $ok, body: $body, old_body: $old_body}}'
