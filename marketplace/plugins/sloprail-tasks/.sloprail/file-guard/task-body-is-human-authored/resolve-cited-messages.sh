#!/usr/bin/env bash
# prepare for stage 2 of task-body-is-human-authored: hand the judge the human's
# own words the body cites, so judge-body.md.j2 never has to parse a transcript
# itself. Receives the SAME CheckPayload the stage-1 script did.
#
# Reached only once stage 1 (has-body-citation.sh) passed — so the body is known
# to carry at least one `[quote](jsonl-path)` link, and every quote is known to
# GROUND via cite to a real user message. This prepare's ONE job is to assemble
# those grounded quotes (and, for an AskUserQuestion answer, the whole question +
# answers envelope) as the judge's ground truth.
#
# WHY THE QUOTES ARE THE GROUND TRUTH. Under the citation-link model the link TEXT
# is the user's own words, already verbatim. cite has confirmed each resolves to a
# real user message, so the quote itself IS what the human said — no separate
# transcript read is needed to recover it. `cite --include-envelope` adds the
# question behind an AskUserQuestion answer, which the answer alone does not carry
# (the same reason no-unasked-deletion's prepare fetches it): a judge cannot weigh
# "the second option" without the question it answered.
#
# Output nests under `additionalContext` — the one key the engine reads from a
# prepare's stdout. Emits .cited_messages (the assembled ground truth), .cited_ok
# (whether any resolved) and .body (the prose the judge rules on — the FRONTMATTER
# is another rule's subject).
#
# THE PREPARE CONTRACT: exit 0 with additionalContext proceeds to the judge; a
# non-zero exit fails the check closed. The old judge FAILED OPEN if it could not
# read the cited messages; a prepare cannot permit-without-judging, so instead it
# reports cited_ok=false to the template, which treats absent ground truth as a
# fail (see the FAIL-OPEN reconciliation in file-guard.yaml).
set -uo pipefail

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"

lib="$gdir/../task-evidence-resolves/cite-links.sh"
if [ ! -f "$lib" ]; then
  echo "task-body-is-human-authored: cite-links.sh not found at $lib, so the cited messages could not be resolved" >&2
  exit 1
fi
# shellcheck source=../task-evidence-resolves/cite-links.sh
. "$lib"

# Same kind dispatch as stage 1, so the two never disagree about which bytes are
# the body. resultKnown is consulted on both Pre kinds before newContent is read.
kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    abs="$root/$path"
    content="$(cat "$abs" 2>/dev/null || true)"
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

# THE BODY IS THE PROSE AFTER THE FRONTMATTER — the same extraction stage 1 uses.
body="$(printf '%s\n' "$content" | awk '
  BEGIN { seen = 0 }
  NR == 1 && $0 == "---" { seen = 1; next }
  seen == 1 && $0 == "---" { seen = 2; next }
  seen == 1 { next }
  { print }
')"

# THE CITED MESSAGES, from the grounded quotes. Each link text is the user's own
# words; cite --include-envelope resolves it and appends the answer envelope when
# the quote is an AskUserQuestion answer (empty for a plain message). Best-effort
# per link: stage 1 already GROUNDED every quote, so an empty result here is this
# prepare's own second lookup failing, not evidence about the body.
cited_messages=""
while IFS="$(printf '\t')" read -r href quote; do
  [ -n "$href" ] || continue
  cpath="$(cite_link_href_path "$href")"
  case "$cpath" in
    /*) : ;;
    *)  cpath="$root/$cpath" ;;
  esac
  # The quote itself is the user's words. Attach the envelope if there is one.
  envelope=""
  if [ -f "$cpath" ]; then
    cite_out="$(sr-session trajectory cite --include-envelope --path "$cpath" "$quote" 2>/dev/null || true)"
    # Drop the first line (the <path>:<line> citation) and the blank line after it;
    # what remains is the envelope, empty for a plain user message.
    envelope="$(printf '%s\n' "$cite_out" | tail -n +3)"
  fi
  cited_messages="${cited_messages}--- the user said (cited ${href}):
${quote}
"
  if [ -n "$envelope" ]; then
    cited_messages="${cited_messages}(this was an answer to a question; the full exchange was:)
${envelope}
"
  fi
  cited_messages="${cited_messages}
"
done <<EOF
$(cite_links_extract "$body")
EOF

cited_ok=false
[ -n "$cited_messages" ] && cited_ok=true

jq -n --arg msgs "$cited_messages" --argjson ok "$cited_ok" --arg body "$body" \
  '{additionalContext: {cited_messages: $msgs, cited_ok: $ok, body: $body}}'
