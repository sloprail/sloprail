#!/usr/bin/env bash
# Stop: a repository with file-guards must commit a CI job that runs `sr-checks verify`, marked by
# a line containing `sr-mark: ci-verify`. Contract: stdin is the GateCheckPayload (unused: the
# decision is about the repository); exit 1 with {"reason": ...} refuses.
set -uo pipefail
cat >/dev/null

MARKER='sr-mark: ci-verify'

# The plugin's root, resolved before the cd below (the gate runs as ./check.sh from its own folder).
plugin_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." 2>/dev/null && pwd -P)"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

# The state this gate last saw for this repository, kept in `sr-session state` (per session): one of
# no-guards, marker-found, refused. A refusal is delivered ONCE per session per state, see the end.
key=
note() { # note <state>: best effort; a pass path never fails over its bookkeeping
  [ -n "$key" ] || return 0
  [ "$(sr-session state get "$key" 2>/dev/null)" = "$1" ] || sr-session state set "$key" "$1" >/dev/null 2>&1 || true
}

cd "${SR_WORKSPACE:-.}" 2>/dev/null || refuse "ci-verify-required could not enter the repository (${SR_WORKSPACE:-.}), so whether CI verifies the file-guards could not be checked"

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  refuse "ci-verify-required: ${SR_WORKSPACE:-.} is not a git repository, so whether CI verifies the file-guards could not be checked"
fi
# An unborn HEAD has no commit for any file-guard to have judged: nothing to verify yet.
git rev-parse --verify -q 'HEAD^{commit}' >/dev/null 2>&1 || exit 0

# Applies only where the project has its OWN file-guards: a committed file under .sloprail/file-guard/.
# Guards a plugin ships are not the project's to enforce in its CI.
top="$(git rev-parse --show-toplevel 2>/dev/null)" || refuse "ci-verify-required could not find the repository root, so its file-guards could not be listed"
own="$(git -C "$top" ls-tree -r --name-only HEAD -- .sloprail/file-guard 2>/dev/null)" || refuse "'git ls-tree' failed in $top, so whether the project has file-guards could not be checked"
fp="$(printf '%s' "$top" | { shasum 2>/dev/null || sha1sum 2>/dev/null || cksum; } | cut -d' ' -f1)"
[ -n "$fp" ] && key="repo:${fp}"
[ -n "$own" ] || { note no-guards; exit 0; }

git grep -q -F -e "$MARKER" HEAD -- 2>/dev/null
case $? in
  0) note marker-found; exit 0 ;;
  1) ;;
  *) refuse "'git grep' failed in ${SR_WORKSPACE:-.}, so whether the committed tree carries a '$MARKER' CI marker could not be checked" ;;
esac

# The release the CI job installs sloprail from: `v<version>` of the plugin installed here (this
# project's entry in installed_plugins.json: its version field, else the plugin.json under its
# installPath), else the version in this plugin's own plugin.json, so CI runs the engine installed here.
installed="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/plugins/installed_plugins.json"
ver=
if [ -n "$plugin_root" ] && [ -r "$installed" ]; then
  entry="$(jq -r --arg root "$plugin_root" '[.plugins[]?[]? | select((.installPath // "") == $root)][0] // empty | [.version // "", .installPath // ""] | @tsv' "$installed" 2>/dev/null)"
  ver="${entry%%$'\t'*}"
  ipath="${entry#*$'\t'}"
  if [ -z "$ver" ] && [ -n "$ipath" ] && [ -r "$ipath/.claude-plugin/plugin.json" ]; then
    ver="$(jq -r '.version // empty' "$ipath/.claude-plugin/plugin.json" 2>/dev/null)"
  fi
fi
if [ -z "$ver" ] && [ -n "$plugin_root" ] && [ -r "$plugin_root/.claude-plugin/plugin.json" ]; then
  ver="$(jq -r '.version // empty' "$plugin_root/.claude-plugin/plugin.json" 2>/dev/null)"
fi
case "$ver" in
  "" | *[!0-9A-Za-z.+-]*) refuse "ci-verify-required could not read the sloprail plugin version, so the CI job's install pin could not be computed" ;;
esac
tag="v${ver#v}"
install="curl -fsSL https://raw.githubusercontent.com/sloprail/sloprail/${tag}/install.sh | SLOPRAIL_INSTALL_TAG=${tag} sh"

msg="ONE-TIME NOTICE, shown once per session for this state of the repository; it will not be repeated, so do not loop on it. Stopping now is allowed. The USER must decide: ask them whether to add the CI job below, or to turn this rule off (list 'sloprail/gate/ci-verify-required' under 'disabled:' in .sloprail/config.yaml). Do not add CI or edit the config without their confirmation.

This project has its own file-guards under .sloprail/file-guard/, but no committed file contains '$MARKER', so nothing shows that CI verifies their verdicts on pull requests and on pushes to the default branch. A file-guard's verdict is only enforced where 'sr-checks verify' runs: on this machine an agent can skip it, in CI it gates the merge.

Add a CI job that runs, on every pull request AND on every push to the default branch,
  pull request:  sr-checks verify --base <merge-base of the target branch and the PR head> --head <PR head sha>
  push to main:  sr-checks verify --base <the push's before sha> --head <the push's after sha>
(the PR's own head, not the provider's merge commit), put the comment '$MARKER' next to that step, and COMMIT it: the check reads the committed tree, not your working copy. 'sr-checks verify' needs the history (a full clone); it fetches the verdicts that 'sr-checks run' stored on the sloprail/checks branch from origin itself and only reads them.

Install sr-checks with the release's install.sh, as below. The snippets pin ${tag}, the release installed here. Plugins the project enables in .claude/settings.json but CI has not installed (such as sloprail itself) are reported on stderr and their rules are NOT verified there; verify checks the project's own rules only, and its exit status is not affected by the missing plugins.

A squash merge needs no re-judging when the pull request was up to date: verify reuses a verdict judged over the same base and head TREES (identical trees are an identical change), so the push to main after squashing a verified PR passes. If main moved while the PR was open, the squash's base tree differs, nothing is reused and the push reads 'not judged yet': squash-merge an up-to-date PR (merge or rebase main into it first), or run 'sr-checks run --base <before sha> --head <after sha>' for that push.

GitHub Actions (.github/workflows/sloprail.yml):
  on:
    pull_request:
    push:
      branches: [main]
  jobs:
    sloprail-verify:
      runs-on: ubuntu-latest
      steps:
        - uses: actions/checkout@v4
          with:
            fetch-depth: 0
            ref: \${{ github.event.pull_request.head.sha || github.sha }}
        - run: |
            ${install}
            echo \"\$HOME/.local/bin\" >> \"\$GITHUB_PATH\"
        # sr-mark: ci-verify
        - env:
            EVENT: \${{ github.event_name }}
            BASE_REF: \${{ github.event.pull_request.base.ref }}
            PR_HEAD: \${{ github.event.pull_request.head.sha }}
            BEFORE: \${{ github.event.before }}
          run: |
            if [ \"\$EVENT\" = pull_request ]; then
              ~/.local/bin/sr-checks verify --base \"origin/\$BASE_REF\" --head \"\$PR_HEAD\"
            else
              # a squash of an up-to-date, verified PR reuses the PR's verdict (same trees); if main moved under the PR it reads 'not judged yet': merge main into the PR before squashing, or run sr-checks run --base \"\$BEFORE\" --head \"\$GITHUB_SHA\"
              # new branch / force push: before is all zeros or a missing object -> merge-base with the default branch, else the root commit
              if [ -z \"\${BEFORE//0/}\" ] || ! git cat-file -e \"\$BEFORE^{commit}\" 2>/dev/null; then
                BEFORE=\$(git merge-base origin/main \"\$GITHUB_SHA\" 2>/dev/null || git rev-list --max-parents=0 \"\$GITHUB_SHA\" | tail -1)
              fi
              ~/.local/bin/sr-checks verify --base \"\$BEFORE\" --head \"\$GITHUB_SHA\"
            fi

GitLab CI (.gitlab-ci.yml):
  sloprail-verify:
    rules:
      - if: \$CI_PIPELINE_SOURCE == \"merge_request_event\"
      - if: \$CI_COMMIT_BRANCH == \$CI_DEFAULT_BRANCH
    variables:
      GIT_DEPTH: \"0\"
    script:
      - ${install}
      # sr-mark: ci-verify
      - |
        if [ -n \"\$CI_MERGE_REQUEST_IID\" ]; then
          # CI_MERGE_REQUEST_DIFF_BASE_SHA is the merge-base of the target and the MR head; the MR head is CI_MERGE_REQUEST_SOURCE_BRANCH_SHA (in merged-results pipelines CI_COMMIT_SHA is a synthetic merge commit), falling back to CI_COMMIT_SHA
          ~/.local/bin/sr-checks verify --base \"\$CI_MERGE_REQUEST_DIFF_BASE_SHA\" --head \"\${CI_MERGE_REQUEST_SOURCE_BRANCH_SHA:-\$CI_COMMIT_SHA}\"
        else
          BEFORE=\"\$CI_COMMIT_BEFORE_SHA\"
          # first pipeline of a branch / force push: all zeros or a missing object -> merge-base with the default branch, else the root commit
          if [ -z \"\${BEFORE//0/}\" ] || ! git cat-file -e \"\$BEFORE^{commit}\" 2>/dev/null; then
            BEFORE=\$(git merge-base \"origin/\$CI_DEFAULT_BRANCH\" \"\$CI_COMMIT_SHA\" 2>/dev/null || git rev-list --max-parents=0 \"\$CI_COMMIT_SHA\" | tail -1)
          fi
          ~/.local/bin/sr-checks verify --base \"\$BEFORE\" --head \"\$CI_COMMIT_SHA\"
        fi

Azure Pipelines (azure-pipelines.yml; add a build validation policy on the default branch):
  trigger: [main]
  pr: [main]
  steps:
    - checkout: self
      fetchDepth: 0
    - script: ${install}
    # sr-mark: ci-verify
    - script: |
        if [ \"\$(Build.Reason)\" = PullRequest ]; then
          ~/.local/bin/sr-checks verify --base origin/\$(System.PullRequest.TargetBranchName) --head \$(System.PullRequest.SourceCommitId)
        else
          # Azure gives no before sha: verify everything the branch added over the default branch (merge-base), or the root commit
          ~/.local/bin/sr-checks verify --base \$(git merge-base origin/main \$(Build.SourceVersion) 2>/dev/null || git rev-list --max-parents=0 \$(Build.SourceVersion) | tail -1) --head \$(Build.SourceVersion)
        fi

(Azure has no push before sha, so branch pushes are verified from the merge-base with the default branch; a build OF the default branch itself has an empty range there, so for it pass the previous successful build's commit as --base if you want per-push ranges.)

Any other provider (Bitbucket Pipelines, Jenkins, CircleCI, ...): run the same 'sr-checks verify' on pull requests (--base the target branch, --head the PR head sha) and on pushes to the default branch (--base the push's before sha, --head its after sha), with the line '$MARKER' in a comment beside it in the committed pipeline file. Jenkinsfile: '// $MARKER'. On the first push of a branch (and on a force push) the before sha is all zeros or missing: use the merge-base of the default branch and the after sha, or the root commit if none, as --base.

To turn this off, list 'sloprail/gate/ci-verify-required' under 'disabled:' in .sloprail/config.yaml."

# Refuse ONCE per session per state: the agent cannot fix this alone (CI and config changes need the
# user's confirmation), so refusing every Stop would loop forever. The state is the repository, whether
# it has its own file-guards, and whether the marker is found; the gate refuses on entering the
# "guards, no marker" state and stays quiet while it lasts. A change (no guards -> guards, marker
# found -> removed) or a new session refuses once again. The refusal is recorded BEFORE it is
# delivered, and a state read/write error says what failed on stderr and lets the Stop through: a
# broken store must not become an infinite loop either.
if [ -z "$key" ] || ! seen="$(sr-session state get "$key" 2>&1)"; then
  echo "ci-verify-required: could not read its session state ($seen), so the one-time notice is not shown; the CI marker is still missing" >&2
  exit 0
fi
[ "$seen" = refused ] && exit 0
if ! out="$(sr-session state set "$key" refused 2>&1)"; then
  echo "ci-verify-required: could not record its session state ($out), so the one-time notice is not shown; the CI marker is still missing" >&2
  exit 0
fi
refuse "$msg"
