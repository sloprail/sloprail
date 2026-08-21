#!/usr/bin/env bash
# prepare for task-review: hand the judge the task's stated outcome and the
# grounded evidence, so review-task.md.j2 never has to parse a transcript itself.
# Receives the SAME CheckPayload the pre-flight did.
#
# Reached only once review-preflight.sh passed — so the task is in_review and every
# `[quote](jsonl)` evidence link is known to GROUND via cite. This prepare's job is
# to assemble those grounded quotes (the user's own words about the work, plus any
# AskUserQuestion envelope) as the evidence the model weighs against the claim.
#
# WHY THE QUOTES ARE THE EVIDENCE. Under the citation-link model the link TEXT is
# the user's own words, already verbatim, and cite has confirmed each resolves to a
# real user message — so the quote IS the evidence, and there is no 4000-line file
# to slice down and no transcript to re-read. `cite --include-envelope` adds the
# question behind an AskUserQuestion answer, which the answer alone does not carry.
#
# Output nests under `additionalContext` — the one key the engine reads. Emits
# .task_body (the whole task, the stated claim), .evidence (the grounded quotes)
# and .evidence_ok (whether any resolved). Both task_body and evidence are agent-
# and user-shaped text and are framed as DATA in the template.
#
# THE PREPARE CONTRACT: exit 0 with additionalContext proceeds to the judge; a
# non-zero exit fails the check closed. The old rule failed OPEN on an evidence-
# read failure; a prepare cannot permit-without-judging, so it reports
# evidence_ok=false to the template, which treats absent evidence as a fail.
set -uo pipefail

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"
abs="$root/$path"

lib="$gdir/../task-evidence-resolves/cite-links.sh"
if [ ! -f "$lib" ]; then
  echo "task-review: cite-links.sh not found at $lib, so the evidence could not be assembled" >&2
  exit 1
fi
# shellcheck source=../task-evidence-resolves/cite-links.sh
. "$lib"

# Post kind only — the pre-flight already established the file is present and
# in_review, so the bytes on disk are the answer.
task_body="$(cat "$abs" 2>/dev/null || true)"

# THE EVIDENCE, from the grounded quotes. Best-effort per link: the pre-flight
# already GROUNDED every quote, so an empty result here is this prepare's own
# second lookup failing, not evidence about the task.
evidence=""
while IFS="$(printf '\t')" read -r href quote; do
  [ -n "$href" ] || continue
  cpath="$(cite_link_href_path "$href")"
  case "$cpath" in
    /*) : ;;
    *)  cpath="$root/$cpath" ;;
  esac
  envelope=""
  if [ -f "$cpath" ]; then
    cite_out="$(sr-session trajectory cite --include-envelope --path "$cpath" "$quote" 2>/dev/null || true)"
    envelope="$(printf '%s\n' "$cite_out" | tail -n +3)"
  fi
  evidence="${evidence}--- the user said (cited ${href}):
${quote}
"
  if [ -n "$envelope" ]; then
    evidence="${evidence}(this was an answer to a question; the full exchange was:)
${envelope}
"
  fi
  evidence="${evidence}
"
done <<EOF
$(cite_links_extract "$task_body")
EOF

evidence_ok=false
[ -n "$evidence" ] && evidence_ok=true

jq -n --arg body "$task_body" --arg ev "$evidence" --argjson ok "$evidence_ok" \
  '{additionalContext: {task_body: $body, evidence: $ev, evidence_ok: $ok}}'
