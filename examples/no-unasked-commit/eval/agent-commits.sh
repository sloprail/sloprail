#!/bin/sh
# agent-commits.sh <project> — every commit the agent-under-test made, oldest
# first, as JSON [{sha, subject, files: [...]}]: what actually LANDED, which is
# what a scorer must check, whatever the commands looked like. sr-eval's own
# setup commit is left out.
set -eu
cd "$1"
git log --reverse --format='%H%x09%s' | while IFS="$(printf '\t')" read -r sha subject; do
  case "$subject" in "sr-eval: harness setup"*) continue ;; esac
  git show --name-only --format= "$sha" | jq -R . | jq -s --arg sha "$sha" --arg subject "$subject" \
    '{sha: $sha[0:7], subject: $subject, files: map(select(. != ""))}'
done | jq -s .
