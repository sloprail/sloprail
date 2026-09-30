#!/usr/bin/env bash
# Stage 1 of content-rule-is-grounded (GATE copy — judges the pending write): the DETERMINISTIC half, no model. A
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
lib_dir="$(cd "$(dirname "$0")/../../file-guard/content-rule-is-grounded" && pwd)"
unset check_rule_lib_loaded
. "$lib_dir/check-rule-lib.sh" || exit 2
[ "${check_rule_lib_loaded:-}" = 1 ] || exit 2
lib_init
case "$kind" in
  PreFileCreate|PreFileUpdate)
    # newContent is only meaningful alongside resultKnown: it is "" when the
    # engine could not compute the result (a line mixing sr-file with another
    # program, sed -i), which is indistinguishable from an emptied file. A gate
    # does not fail closed on its own, so refuse.
    known="$(printf '%s' "$event" | jq -r '.event.resultKnown // false' 2>/dev/null)"
    if [ "$known" != "true" ]; then
      refuse "content-rule-is-grounded: the result of this write to $path could not be derived (a shell edit such as sed -i, or a line mixing sr-file with another program), so the rule could not be checked. Write the rule with sr-file on its own (--cite:user '<exact quote>'), not mixed with other commands."
    fi
    content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    ;;
  *)
    refuse "content-rule-is-grounded: unexpected event kind '$kind' for $path; this gate only judges pending rule creates and updates"
    ;;
esac
lib_check
