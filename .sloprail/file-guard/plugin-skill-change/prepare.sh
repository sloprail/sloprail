#!/usr/bin/env bash
# prepare: the change to the skill this subject names, as additionalContext {skill, paths, diff}:
# the diff of its files over the range.
set -uo pipefail
payload="$(cat)"
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/changeset.sh"
skill="$(subject_id)"
case "$skill" in '' | unclaimed) echo '{"skip": true}'; exit 0 ;; esac
printf '%s' "$payload" | jq -c --arg s "$skill" '.subject.files as $f
  | {additionalContext: {skill: $s,
      paths: ($f | join("\n")),
      diff: ([.changeset.files[] | select(.path as $p | $f | index($p)) | .diff // ""] | join("\n"))}}' ||
  refuse "the change to the skill $skill could not be read, so it could not be judged"
