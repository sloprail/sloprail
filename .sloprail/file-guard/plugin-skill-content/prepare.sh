#!/usr/bin/env bash
# prepare: the skill this subject names, whole, and its brief, as additionalContext {skill, brief,
# files: [{path, content}]}. Read from the committed tree. A skill with no brief is refused: what
# it covers has to be written down before its content can be judged against it.
set -uo pipefail
payload="$(cat)"
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/changeset.sh"
skill="$(subject_id)"
case "$skill" in '' | unclaimed) echo '{"skip": true}'; exit 0 ;; esac
dir="marketplace/plugins/sloprail/skills/$skill"
brief=".sloprail/file-guard/plugin-skill-content/briefs/$skill.md"
[ -f "$SR_TREE/$brief" ] ||
  refuse "the skill $skill has no brief. Write $brief: who reads the skill, what it is for, and what is in and out of its scope (see the other briefs there)."
[ -d "$SR_TREE/$dir" ] || { echo '{"skip": true}'; exit 0; }
files="$(cd "$SR_TREE" && for f in "$dir"/*; do [ -f "$f" ] && jq -n --arg p "$f" --rawfile c "$f" '{path: $p, content: $c}'; done | jq -sc .)" ||
  refuse "the files of $dir could not be read, so the skill could not be judged"
jq -nc --arg s "$skill" --rawfile b "$SR_TREE/$brief" --argjson f "$files" \
  '{additionalContext: {skill: $s, brief: $b, files: $f}}'
