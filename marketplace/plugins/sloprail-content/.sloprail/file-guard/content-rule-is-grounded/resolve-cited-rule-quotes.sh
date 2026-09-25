#!/usr/bin/env bash
# prepare for stage 2 of content-rule-is-grounded: hand the judge the human's
# own words the rule's body cites, so judge-rule-body.md.j2 never has to
# parse a transcript itself. Mirrors sloprail-tasks's
# resolve-cited-messages.sh exactly — same shape, same reasoning — for a
# rule's body instead of a task's.
#
# Reached only once stage 1 (check-rule.sh) passed — so the body is known to
# carry at least one grounded [quote](jsonl-path) link. This prepare's ONE
# job is to assemble those grounded quotes as the judge's ground truth.
#
# THE PREPARE CONTRACT: exit 0 with additionalContext proceeds to the judge;
# non-zero fails the check closed.
set -uo pipefail

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"

lib="$gdir/../unit-publish-approved/cite-links.sh"
if [ ! -f "$lib" ]; then
  echo "content-rule-is-grounded: cite-links.sh not found at $lib, so the cited messages could not be resolved" >&2
  exit 1
fi
# shellcheck source=../unit-publish-approved/cite-links.sh
. "$lib"

kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    ;;
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$event" | jq -r '.event.resultKnown // false' 2>/dev/null)"
    if [ "$known" != "true" ]; then
      content=""
    else
      content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    fi
    ;;
  *)
    content=""
    ;;
esac

body="$(printf '%s\n' "$content" | awk '
  BEGIN { seen = 0 }
  NR == 1 && $0 == "---" { seen = 1; next }
  seen == 1 && $0 == "---" { seen = 2; next }
  seen == 1 { next }
  { print }
')"

cited_messages=""
while IFS="$(printf '\t')" read -r href quote; do
  [ -n "$href" ] || continue
  cpath="$(cite_link_href_path "$href")"
  case "$cpath" in
    /*) : ;;
    *)  cpath="$root/$cpath" ;;
  esac
  cited_messages="${cited_messages}--- the user said (cited ${href}):
${quote}

"
done <<EOF
$(cite_links_extract "$body")
EOF

cited_ok=false
[ -n "$cited_messages" ] && cited_ok=true

jq -n --arg msgs "$cited_messages" --argjson ok "$cited_ok" --arg body "$body" \
  '{additionalContext: {cited_messages: $msgs, cited_ok: $ok, body: $body}}'
