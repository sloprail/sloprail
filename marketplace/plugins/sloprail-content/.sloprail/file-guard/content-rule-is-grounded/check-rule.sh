#!/usr/bin/env bash
# Stage 1 of content-rule-is-grounded (file-guard entry — judges the committed rules):
# the DETERMINISTIC half, no model. A writing RULE
# (`.sloprail/content-rules/<NN>/RULE.md` or a topic's
# `constraints/<NN>/CONSTRAINT.md`) must have frontmatter satisfying
# .sloprail/schemas/rule.cue and a non-empty body stating the rule.
#
# GROUNDING IS NOT CHECKED HERE. The guard's `require: [{citation: {source_types: [user]}}]`
# already refused a changeset whose commits carry no citation of the user's own
# words (`Sloprail-Cites-User:` trailers) before this script runs, and stage 2's
# judge decides whether the cited words actually ground the rule. So this script
# reads no citation and parses no link: a body may still carry an old
# `[quote](jsonl)` link from before the migration, and that is neither required nor
# refused.
#
# REFUSAL CONTRACT: exit 0 permits; non-zero refuses with `{"reason": "..."}` on
# stdout. `set -uo pipefail`, never `set -e`.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset check_rule_lib_loaded
. "$lib_dir/check-rule-lib.sh" || exit 2
[ "${check_rule_lib_loaded:-}" = 1 ] || exit 2
lib_setup

event="$(cat)"
[ "$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)" = "Changeset" ] ||
  refuse "content-rule-is-grounded: expected a Changeset event, so the rules could not be checked"
n="$(printf '%s' "$event" | jq -r '.changeset.files | length')" ||
  refuse "content-rule-is-grounded: the changeset's files could not be read, so the rules could not be checked"
case "$n" in '' | *[!0-9]*) refuse "content-rule-is-grounded: the changeset's files could not be read, so the rules could not be checked" ;; esac

i=0
while [ "$i" -lt "$n" ]; do
  path="$(printf '%s' "$event" | jq -r --argjson i "$i" '.changeset.files[$i].path')" ||
    refuse "content-rule-is-grounded: could not read file $i of the changeset"
  content="$(printf '%s' "$event" | jq -r --argjson i "$i" '.changeset.files[$i].newContent')" ||
    refuse "content-rule-is-grounded: could not read $path from the changeset"
  i=$((i + 1))
  [ -n "$path" ] || refuse "content-rule-is-grounded: a file of the changeset named no path, so there is nothing to check"
  lib_check
done
exit 0
