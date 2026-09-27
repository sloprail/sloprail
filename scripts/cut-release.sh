#!/usr/bin/env bash
# Opens the version-bump PR that starts a release, running as YOU rather than
# as a GitHub Actions workflow.
#
# WHY THIS IS A LOCAL SCRIPT AND NOT A GITHUB ACTION. It used to be one
# (.github/workflows/cut-release.yml, removed in the same change that added
# this): workflow_dispatch, bump, commit, push a branch, open a PR. It could
# not work. Two independent GitHub Actions restrictions, discovered by
# actually running it:
#
#   1. main's ruleset requires every change to arrive as a reviewed PR — a
#      direct push, even from the workflow's own GITHUB_TOKEN, is rejected
#      (GH013). Fixed by having the workflow open a PR instead of pushing to
#      main directly.
#   2. A push made with the default GITHUB_TOKEN never triggers OTHER
#      workflow runs — GitHub's deliberate anti-recursion rule. test.yml's
#      `on: push` (which reports the 9 checks main's ruleset requires) NEVER
#      FIRES for a workflow-pushed branch, so the bump PR's required checks
#      sit at "expected" forever and the PR can never merge without an
#      explicit admin override on every single release.
#
# (2) has no fix that keeps this a zero-secrets GitHub Action: the standard
# answer is a PAT or GitHub App token pushing in its place, which is a real
# new secret to hold and rotate for something that happens a few times a
# year. Running this locally sidesteps it entirely — a push from an actual
# GitHub account (yours) is not GITHUB_TOKEN, so test.yml fires normally, no
# secret required.
#
# tag-after-release-bump.yml stays a real GitHub Action: it reacts to a PR
# *merge* (a pull_request event, not a push this script makes), which is not
# subject to either restriction above.
set -euo pipefail

if [ $# -ne 1 ]; then
  echo "usage: $0 <version>  (e.g. 0.2.1, no leading v)" >&2
  exit 1
fi
version="$1"

case "$version" in
v*) echo "cut-release.sh: pass bare semver, no leading v (got '$version')" >&2; exit 1 ;;
esac

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

if ! command -v gh >/dev/null 2>&1; then
  echo "cut-release.sh: the GitHub CLI (gh) is required to open the bump PR — https://cli.github.com" >&2
  exit 1
fi

if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "cut-release.sh: working tree is not clean — commit or stash first" >&2
  exit 1
fi

default_branch="$(git symbolic-ref --quiet --short refs/remotes/origin/HEAD | sed 's|^origin/||' || echo main)"
git fetch origin "$default_branch"

branch="release/v${version}"
git checkout -b "$branch" "origin/$default_branch"

./scripts/bump-version.sh "$version"

if git diff --quiet; then
  echo "cut-release.sh: nothing changed — plugin.json/marketplace.json already say ${version}" >&2
  git checkout "$default_branch"
  git branch -D "$branch"
  exit 1
fi

# Same gate release.yml itself runs, against the WORKING TREE (verify-version
# reads plugin.json/marketplace.json off disk either way) — catches a bump
# that missed a file before a PR is even opened, rather than after review.
make verify-version TAG="v${version}"

git add -A
git commit -m "chore(release): bump plugins to v${version}"
git push origin "$branch"

gh pr create \
  --title "chore(release): bump plugins to v${version}" \
  --body "Bumps plugin.json/marketplace.json to v${version}. Merging this (squash, so the commit on main keeps this exact title) is the last step before tagging — tag-after-release-bump.yml tags main automatically once it does." \
  --base "$default_branch" \
  --head "$branch"

git checkout "$default_branch"
