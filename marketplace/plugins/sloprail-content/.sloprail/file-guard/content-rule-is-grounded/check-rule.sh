#!/usr/bin/env bash
# Stage 1 of content-rule-is-grounded: the DETERMINISTIC half, no model. A
# writing RULE (`.sloprail/content-rules/<NN>/RULE.md` or a topic's
# `constraints/<NN>/CONSTRAINT.md`) must be traceable to something a human
# actually asked for — the rule-authoring analogue of
# task-body-is-human-authored's own stage 1, and the SAME mechanism: the
# rule's BODY (not a frontmatter field — an earlier draft's transcript_paths
# was dropped, see rule.cue and the plugin README's migration note) must
# carry at least one `[quote](jsonl)` markdown link whose quote is the user's
# own words and resolves via `sr-session trajectory cite --source-types
# user`. An agent inventing a rule nobody asked for — "always mention the
# product name" — and citing nothing, or citing its own prior turn, is
# refused here, deterministically, before the judge (stage 2,
# resolve-cited-rule-quotes.sh + judge-rule-body.md.j2) is ever paid for.
#
# cite-links.sh (cite_links_extract / cite_link_href_path / cite_ground) is
# SOURCED from the sibling unit-publish-approved guard, not reimplemented —
# the two guards must agree exactly about what a citation link IS and how it
# grounds, the same relative-sibling-sourcing sloprail-tasks's own
# task-body-is-human-authored uses for task-evidence-resolves's copy.
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
  refuse "content-rule-is-grounded: the event named no path, so there is nothing to check"
fi

root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"

schema="$root/.sloprail/schemas/rule.cue"
if [ ! -f "$schema" ]; then
  refuse "content-rule-is-grounded: schema not found at $schema — install the plugin's rule.cue under the project's .sloprail/schemas/."
fi

lib="$gdir/../unit-publish-approved/cite-links.sh"
if [ ! -f "$lib" ]; then
  refuse "content-rule-is-grounded: cite-links.sh not found at $lib — the deterministic half cannot run without the sibling guard's citation library"
fi
# shellcheck source=../unit-publish-approved/cite-links.sh
. "$lib"

kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    # A Post kind carries the SETTLED bytes directly on the flat event — no
    # disk re-read.
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

if ! doc="$(printf '%s' "$content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)"; then
  detail="$(printf '%s' "$doc" | sed "s|^-:|$path:|g")"
  refuse "RULE FRONTMATTER INVALID: $path does not satisfy .sloprail/schemas/rule.cue.

$detail"
fi

# THE BODY IS THE PROSE AFTER THE FRONTMATTER — same extraction every guard
# in this plugin uses.
body="$(printf '%s\n' "$content" | awk '
  BEGIN { seen = 0 }
  NR == 1 && $0 == "---" { seen = 1; next }
  seen == 1 && $0 == "---" { seen = 2; next }
  seen == 1 { next }
  { print }
')"

body_trimmed="$(printf '%s' "$body" | tr -d '[:space:]')"
if [ -z "$body_trimmed" ]; then
  refuse "RULE BODY IS EMPTY: $path has no body stating the rule and citing where it came from."
fi

# EVERY CITATION LINK IN THE BODY MUST GROUND, exactly as task-body-is-human-
# authored's stage 1 checks a task's ask citation.
problems=""
n_ok=0
while IFS="$(printf '\t')" read -r href quote; do
  [ -n "$href" ] || continue
  cpath="$(cite_link_href_path "$href")"
  case "$cpath" in
    /*) : ;;
    *)  cpath="$root/$cpath" ;;
  esac
  if reason="$(cite_ground user "$cpath" "$quote")"; then
    n_ok=$((n_ok + 1))
  else
    problems="${problems}  ${reason}
"
  fi
done <<EOF
$(cite_links_extract "$body")
EOF

if [ "$n_ok" -eq 0 ] && [ -z "$problems" ]; then
  refuse "RULE NOT GROUNDED: $path carries no citation of a user message.

A writing rule must be derived from something a human actually said, and must quote and link it:

    Rule: no em-dashes. The user said [never use em-dashes](/abs/session.jsonl:42).

The quote must resolve — via sr-session trajectory cite — to a real user message. A rule with no origin citation is indistinguishable from one an agent invented on its own."
fi

if [ -n "$problems" ]; then
  refuse "RULE CITES SOMETHING THE USER DID NOT SAY: $path

$problems
A body citation is a markdown link [<quote>](<jsonl-path>) whose quote is the user's own words, verbatim, and whose href is the transcript they said them in. It must resolve — via cite — to a real user message. A paraphrase, a fabrication, a tool result, or a harness-injected message is not the human's ask. Cite the line where the user actually spoke."
fi

exit 0
