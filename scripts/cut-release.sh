#!/usr/bin/env bash
# Runs the release flow as YOU, not as a GitHub Actions workflow — both
# halves of it: `open` starts a release (bump, commit, push, open the PR);
# `tag` finishes one, once you've merged that PR, by tagging main (which
# triggers release.yml to actually build and publish).
#
# WHY THIS IS A LOCAL SCRIPT AND NOT A GITHUB ACTION, for BOTH halves. Each
# used to be one (.github/workflows/cut-release.yml and
# tag-after-release-bump.yml, removed in the same change that added this).
# Neither could work, for the same underlying reason discovered by actually
# running each: a push made with the default GITHUB_TOKEN never triggers
# OTHER workflow runs — GitHub's deliberate anti-recursion rule, and it
# applies to a tag push exactly as it does to a branch push.
#
#   `open`'s bump PR, pushed by a workflow, meant test.yml's `on: push` (which
#   reports the checks main's ruleset requires before a PR can merge) never
#   fired — the PR's required checks sat at "expected" forever.
#
#   `tag`'s v0.2.1 tag, pushed by tag-after-release-bump.yml, meant
#   release.yml's `on: push: tags: v*` never fired either — proven by
#   actually shipping v0.2.1 this way: the tag existed on GitHub, nothing
#   ever built or published it.
#
# The standard fix for either is a PAT or GitHub App token pushing in the
# workflow's place — a real secret to hold and rotate for something that
# happens a few times a year. Running BOTH steps locally sidesteps it
# entirely: a push from a real GitHub account is not GITHUB_TOKEN, so
# test.yml and release.yml both fire normally, no secret required.
set -euo pipefail

usage() {
  echo "usage: $0 open <version>   start a release: bump, commit, push, open the PR" >&2
  echo "       $0 tag <version>    after merging that PR: tag main, which triggers release.yml" >&2
  echo "  <version> is bare semver, e.g. 0.2.1, no leading v" >&2
  exit 1
}

[ $# -eq 2 ] || usage
verb="$1"
version="$2"

case "$verb" in
open | tag) ;;
*) usage ;;
esac

case "$version" in
v*) echo "cut-release.sh: pass bare semver, no leading v (got '$version')" >&2; exit 1 ;;
esac

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

if ! command -v gh >/dev/null 2>&1; then
  echo "cut-release.sh: the GitHub CLI (gh) is required — https://cli.github.com" >&2
  exit 1
fi

if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "cut-release.sh: working tree is not clean — commit or stash first" >&2
  exit 1
fi

default_branch="$(git symbolic-ref --quiet --short refs/remotes/origin/HEAD | sed 's|^origin/||' || echo main)"
git fetch origin "$default_branch"

case "$verb" in
open)
  branch="release/v${version}"
  git checkout -b "$branch" "origin/$default_branch"

  ./scripts/bump-version.sh "$version"

  if git diff --quiet; then
    echo "cut-release.sh: nothing changed — plugin.json/marketplace.json already say ${version}" >&2
    git checkout "$default_branch"
    git branch -D "$branch"
    exit 1
  fi

  # Same gate release.yml itself runs, against the WORKING TREE
  # (verify-version reads plugin.json/marketplace.json off disk either way)
  # — catches a bump that missed a file before a PR is even opened, rather
  # than after review.
  make verify-version TAG="v${version}"

  git add -A
  git commit -m "chore(release): bump plugins to v${version}"
  git push origin "$branch"

  gh pr create \
    --title "chore(release): bump plugins to v${version}" \
    --body "Bumps plugin.json/marketplace.json to v${version}. Merging this (squash, so the commit on main keeps this exact title) is the last step before tagging — once merged, run \`$(basename "$0") tag ${version}\` to tag main and kick off release.yml." \
    --base "$default_branch" \
    --head "$branch"

  git checkout "$default_branch"
  ;;

tag)
  # Verified against origin/$default_branch, not the local working tree
  # (which may be on another branch, or simply not yet pulled) — the tag is
  # going to point at that remote commit, so that is the plugin.json/
  # marketplace.json this has to agree with, not whatever happens to be
  # checked out here right now.
  . "$(dirname "$0")/plugin-manifest-dirs.sh"
  got=""
  for d in $plugin_manifest_dirs; do
    got="$(git show "origin/$default_branch:marketplace/plugins/sloprail/$d/plugin.json" 2>/dev/null | jq -r .version || true)"
    [ -n "$got" ] && break
  done
  if [ "$got" != "$version" ]; then
    echo "cut-release.sh: origin/$default_branch's plugin.json says $got, not $version — has the bump PR (release/v${version}) actually been merged yet?" >&2
    exit 1
  fi

  git tag "v${version}" "origin/$default_branch"
  git push origin "v${version}"
  ;;

*)
  usage
  ;;
esac
