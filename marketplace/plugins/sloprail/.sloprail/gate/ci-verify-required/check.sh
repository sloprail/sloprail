#!/usr/bin/env bash
# Stop: a repository with file-guards must commit a CI job that runs `sr-checks verify`, marked by
# a line containing `sr-mark: ci-verify`. Contract: stdin is the GateCheckPayload (unused: the
# decision is about the repository); exit 1 with {"reason": ...} refuses.
set -uo pipefail
cat >/dev/null

MARKER='sr-mark: ci-verify'

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
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
[ -n "$own" ] || exit 0

git grep -q -F -e "$MARKER" HEAD -- 2>/dev/null
case $? in
  0) exit 0 ;;
  1) ;;
  *) refuse "'git grep' failed in ${SR_WORKSPACE:-.}, so whether the committed tree carries a '$MARKER' CI marker could not be checked" ;;
esac

# The sloprail revision the CI job installs sr-checks from: the commit this plugin was installed
# from (Claude Code records it in installed_plugins.json), so CI runs the engine that is installed
# here; `main` when that cannot be read. No release tarball carries sr-checks yet, hence `go install`.
ref=main
plugin_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." 2>/dev/null && pwd -P)"
installed="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/plugins/installed_plugins.json"
if [ -n "$plugin_root" ] && [ -r "$installed" ]; then
  sha="$(jq -r --arg root "$plugin_root" '[.plugins[]?[]? | select((.installPath // "") == $root) | .gitCommitSha // empty][0] // empty' "$installed" 2>/dev/null)"
  case "$sha" in
    *[!0-9a-f]* | "") ;;
    *) [ "${#sha}" -eq 40 ] && ref="$sha" ;;
  esac
fi

refuse "This project has its own file-guards under .sloprail/file-guard/, but no committed file contains '$MARKER', so nothing shows that CI verifies their verdicts on pull requests and on pushes to the default branch. A file-guard's verdict is only enforced where 'sr-checks verify' runs: on this machine an agent can skip it, in CI it gates the merge.

Add a CI job that runs, on every pull request AND on every push to the default branch,
  pull request:  sr-checks verify --base <merge-base of the target branch and the PR head> --head <PR head sha>
  push to main:  sr-checks verify --base <the push's before sha> --head <the push's after sha>
(the PR's own head, not the provider's merge commit), put the comment '$MARKER' next to that step, and COMMIT it: the check reads the committed tree, not your working copy. 'sr-checks verify' needs the history (a full clone); it fetches the verdicts that 'sr-checks run' stored on the sloprail/checks branch from origin itself and only reads them.

Install sr-checks with Go, as below: no sloprail release tarball carries sr-checks yet, so install.sh would leave the job without it. The snippets pin ${ref}, the revision installed here. Plugins the project enables in .claude/settings.json but CI has not installed (such as sloprail itself) are reported on stderr and their rules are NOT verified there; verify checks the project's own rules only, and its exit status is not affected by the missing plugins.

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
        - uses: actions/setup-go@v5
          with:
            go-version: '1.25'
        - run: GOBIN=\"\$HOME/.local/bin\" go install github.com/sloprail/sloprail/services/sr-checks@${ref}
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
    image: golang:1.25
    script:
      - GOBIN=\"\$HOME/.local/bin\" go install github.com/sloprail/sloprail/services/sr-checks@${ref}
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
    - task: GoTool@0
      inputs:
        version: '1.25'
    - script: GOBIN=\"\$HOME/.local/bin\" go install github.com/sloprail/sloprail/services/sr-checks@${ref}
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
