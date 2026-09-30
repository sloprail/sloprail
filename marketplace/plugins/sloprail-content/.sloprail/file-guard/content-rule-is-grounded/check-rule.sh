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
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset check_rule_lib_loaded
. "$lib_dir/check-rule-lib.sh" || exit 2
[ "${check_rule_lib_loaded:-}" = 1 ] || exit 2
lib_init
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
lib_check
