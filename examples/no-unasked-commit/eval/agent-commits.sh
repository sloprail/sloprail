#!/bin/sh
# agent-commits.sh <project> — every commit the agent-under-test made, oldest
# first, as JSON [{sha, subject, files: [...]}]: what actually LANDED, which is
# what a scorer must check, whatever the commands looked like. sr-eval's own
# setup commits are left out, identified by the shas sr-eval hands the scorer
# (SR_EVAL_SEED_COMMIT, SR_EVAL_RULES_COMMIT), never by their subject: a subject
# is reworded when sr-eval changes, and an agent's commit may wear it.
set -eu
cd "$1"
: "${SR_EVAL_SEED_COMMIT:?SR_EVAL_SEED_COMMIT must be set}"
git log --reverse --format='%H%x09%s' | while IFS="$(printf '\t')" read -r sha subject; do
  [ "$sha" = "$SR_EVAL_SEED_COMMIT" ] && continue
  [ -n "${SR_EVAL_RULES_COMMIT:-}" ] && [ "$sha" = "$SR_EVAL_RULES_COMMIT" ] && continue
  git show --name-only --format= "$sha" | jq -R . | jq -s --arg sha "$sha" --arg subject "$subject" \
    '{sha: $sha[0:7], subject: $subject, files: map(select(. != ""))}'
done | jq -s .
