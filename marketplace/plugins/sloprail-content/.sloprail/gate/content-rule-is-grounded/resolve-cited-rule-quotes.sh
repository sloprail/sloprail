#!/usr/bin/env bash
# prepare for stage 2 of content-rule-is-grounded: hand the judge the rule's
# body as it will stand, for context. What the judge rules on — the change — and
# the cited words need no preparing: judge-rule-body.md.j2 reads `change` and
# `.event.citations` straight off its input.
#
# THE PREPARE CONTRACT: exit 0 with additionalContext proceeds to the judge;
# non-zero fails the check closed. Every read of the payload goes through
# field(), which fails the prepare (and so the check) on an unreadable payload
# rather than handing the judge an empty body as if the rule had none.
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
case "$kind" in
  PreFileCreate|PreFileUpdate)
    # newContent is only meaningful alongside resultKnown. check-rule.sh runs
    # first and refuses an unknown result, so the judge never sees one; should
    # this prepare run without it, fail closed rather than hand the judge an
    # empty body as if the rule had none.
    [ "$(field '.event.resultKnown // false')" = "true" ] ||
      fail "the result of this write could not be derived, so the rule body is unseen"
    content="$(field '.event.newContent // ""')" || exit 1
    ;;
  *)
    fail "unexpected event kind '$kind'; this gate judges only pending rule creates and updates"
    ;;
esac

jq -n --arg body "$(body_of "$content")" '{additionalContext: {body: $body}}'
