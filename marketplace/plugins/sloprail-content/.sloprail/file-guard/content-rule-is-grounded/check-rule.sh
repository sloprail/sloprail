#!/usr/bin/env bash
# Stage 1 of content-rule-is-grounded (file-guard copy — judges the settled file at Stop): the DETERMINISTIC half, no model. A
# writing RULE (`.sloprail/content-rules/<NN>/RULE.md` or a topic's
# `constraints/<NN>/CONSTRAINT.md`) must have frontmatter satisfying
# .sloprail/schemas/rule.cue and a non-empty body stating the rule.
#
# GROUNDING IS NOT CHECKED HERE. The guard's `require: [{citation: {source_types: [user]}}]`
# already refused any change carrying no citation of the user's own words
# before this script runs (the citation rides on the `sr-file ... --cite:user`
# command, never in the file), and stage 2's judge decides whether the cited
# words actually ground the rule. So this script reads no citation and parses
# no link: a body may still carry an old `[quote](jsonl)` link from before the
# migration, and that is neither required nor refused.
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

# The schema is the PLUGIN's, read from its own tree — this guard's folder is two
# levels under the plugin's .sloprail/ — never a consumer-side copy.
schema="${SR_GUARDRAIL_DIR:-.}/../../schemas/rule.cue"
if [ ! -f "$schema" ]; then
  refuse "content-rule-is-grounded: schema not found at $schema — the plugin's own rule.cue is missing, so no rule can be checked."
fi

kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    # A Post kind carries the SETTLED bytes directly on the flat event — no
    # disk re-read — when the engine could read them. It says so in
    # newContentKnown, a bool the engine declares on PostFileCreate and
    # PostFileUpdate (internal/filemod/module.go FieldNewContentKnown;
    # authoring-guardrails/events.md): false for a link to a FIFO or a device,
    # or a file past the read cap. Unread: refuse, unchecked.
    if [ "$(printf '%s' "$event" | jq -r '.event.newContentKnown // false' 2>/dev/null)" != "true" ]; then
      refuse "content-rule-is-grounded: $path could not be read (not a regular file, or too large), so the rule could not be checked"
    fi
    content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    ;;
  *)
    refuse "content-rule-is-grounded: unexpected event kind '$kind' for $path; this rule only judges settled rule creates and updates"
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
  refuse "RULE BODY IS EMPTY: $path has no body stating the rule. Write the rule itself after the frontmatter, in words derived from what the user said; the citation of their words goes on the sr-file command (--cite:user '<exact quote>'), not in the file."
fi

exit 0
