#!/usr/bin/env bash
# Stop: a repository with file-guards must commit a CI job that runs `sr-checks verify`, marked by
# the sloprail marker `sr:ci verify` (written by `sr-mark apply ci --verify=<path>:<line>`, read by
# `sr-mark find`, the engine's own marker reader). Contract: stdin is the GateCheckPayload (unused: the
# decision is about the repository); exit 1 with {"reason": ...} refuses.
set -uo pipefail
cat >/dev/null

MARKER='sr:ci verify'

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

command -v sr-mark >/dev/null 2>&1 || refuse "sr-mark is not on PATH, so whether the committed tree carries a '$MARKER' CI marker could not be checked. Install the sloprail plugin's binaries."
# `sr-mark find` reads the COMMITTED tree at HEAD with the engine's marker reader: 0 found, 1 none,
# anything else could not be read (a refusal, never a pass).
sr-mark find ci --fqn verify --rev HEAD --root "$top" >/dev/null 2>"${TMPDIR:-/tmp}/sr-ci-find.$$"
case $? in
  0) rm -f "${TMPDIR:-/tmp}/sr-ci-find.$$"; exit 0 ;;
  1) rm -f "${TMPDIR:-/tmp}/sr-ci-find.$$" ;;
  *) why="$(cat "${TMPDIR:-/tmp}/sr-ci-find.$$" 2>/dev/null)"; rm -f "${TMPDIR:-/tmp}/sr-ci-find.$$"
     refuse "'sr-mark find' failed in $top, so whether the committed tree carries a '$MARKER' CI marker could not be checked: $why" ;;
esac

# The sloprail revision the CI job installs sr-checks from: the commit this plugin was installed
# from (Claude Code records it in installed_plugins.json), so CI runs the engine that is installed
# here; `main` when that cannot be read. No release tarball carries sr-checks yet, hence `go install`.
ref=main
plugin_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." 2>/dev/null && pwd -P)"
installed="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/plugins/installed_plugins.json"
if [ -n "$plugin_root" ] && [ -r "$installed" ]; then
  # The entry for THIS project (projectPath = the repository root); a user-scope entry (no
  # projectPath) next; never another project's entry that shares the installPath.
  sha="$(jq -r --arg root "$plugin_root" --arg top "$top" '
    [.plugins[]?[]? | select((.installPath // "") == $root)] as $e
    | (([$e[] | select((.projectPath // "") == $top)][0]) // ([$e[] | select((.projectPath // "") == "")][0]) // {}) | .gitCommitSha // empty' "$installed" 2>/dev/null)"
  case "$sha" in
    *[!0-9a-f]* | "") ;;
    *) [ "${#sha}" -eq 40 ] && ref="$sha" ;;
  esac
fi

refuse "This project has its own file-guards under .sloprail/file-guard/, but no committed file carries the marker '$MARKER', so nothing shows that CI verifies their verdicts on pull requests. A file-guard's verdict is only enforced where 'sr-checks verify' runs: on this machine an agent can skip it, in CI it gates the merge.

Add a CI job that runs, on every pull request,
  sr-checks verify --base <the target branch (it uses the merge-base with the PR head)> --head <PR head sha>
(the PR's own head, not the provider's merge commit), put the marker '$MARKER' on its own comment line next to that step (write it with: sr-mark apply ci --verify=<path>:<line>), and COMMIT it: the check reads the committed tree, not your working copy. 'sr-checks verify' needs the history (a full clone); it fetches the verdicts that 'sr-checks run' stored on the sloprail/checks branch from origin itself and only reads them. Make the job a required status check.

Protect the default branch: require pull requests and up-to-date branches, and allow no direct pushes. Then the pull request's range is exactly what lands, and verify on pull requests is the guarantee; no job on pushes to the default branch is needed.

Install sr-checks with Go, as below: no sloprail release tarball carries sr-checks yet, so install.sh would leave the job without it. The snippets pin ${ref}, the revision installed here. Plugins the project enables in .claude/settings.json but CI has not installed (such as sloprail itself) are reported on stderr and their rules are NOT verified there; verify checks the project's own rules only, and its exit status is not affected by the missing plugins.

GitHub Actions (.github/workflows/sloprail.yml):
  on:
    pull_request:
  jobs:
    sloprail-verify:
      runs-on: ubuntu-latest
      steps:
        - uses: actions/checkout@v4
          with:
            fetch-depth: 0
            ref: \${{ github.event.pull_request.head.sha }}
        - uses: actions/setup-go@v5
          with:
            go-version: '1.25'
        - run: GOBIN=\"\$HOME/.local/bin\" go install github.com/sloprail/sloprail/services/sr-checks@${ref}
        # sr:ci verify
        - env:
            BASE_REF: \${{ github.event.pull_request.base.ref }}
            PR_HEAD: \${{ github.event.pull_request.head.sha }}
          run: ~/.local/bin/sr-checks verify --base \"origin/\$BASE_REF\" --head \"\$PR_HEAD\"

GitLab CI (.gitlab-ci.yml):
  sloprail-verify:
    rules:
      - if: \$CI_PIPELINE_SOURCE == \"merge_request_event\"
    variables:
      GIT_DEPTH: \"0\"
    image: golang:1.25
    script:
      - GOBIN=\"\$HOME/.local/bin\" go install github.com/sloprail/sloprail/services/sr-checks@${ref}
      # sr:ci verify
      # CI_MERGE_REQUEST_DIFF_BASE_SHA is the merge-base of the target and the MR head; the MR head is CI_MERGE_REQUEST_SOURCE_BRANCH_SHA (in merged-results pipelines CI_COMMIT_SHA is a synthetic merge commit), falling back to CI_COMMIT_SHA
      - ~/.local/bin/sr-checks verify --base \"\$CI_MERGE_REQUEST_DIFF_BASE_SHA\" --head \"\${CI_MERGE_REQUEST_SOURCE_BRANCH_SHA:-\$CI_COMMIT_SHA}\"

Azure Pipelines (azure-pipelines.yml; add a build validation policy on the default branch):
  trigger: none
  pr: [main]
  steps:
    - checkout: self
      fetchDepth: 0
    - task: GoTool@0
      inputs:
        version: '1.25'
    - script: GOBIN=\"\$HOME/.local/bin\" go install github.com/sloprail/sloprail/services/sr-checks@${ref}
    # sr:ci verify
    - script: ~/.local/bin/sr-checks verify --base origin/\$(System.PullRequest.TargetBranchName) --head \$(System.PullRequest.SourceCommitId)

Any other provider (Bitbucket Pipelines, Jenkins, CircleCI, ...): run the same 'sr-checks verify' on pull requests (--base the target branch, --head the PR head sha), with the marker line '$MARKER' in a comment beside it in the committed pipeline file. Jenkinsfile: '// $MARKER'. (The file-guard 'sloprail/file-guard/ci-verify-step' checks the marked file's pull request trigger for GitHub Actions, GitLab CI and Azure Pipelines; in any other file it requires only a line that runs 'sr-checks verify'.)

To turn this off, list 'sloprail/gate/ci-verify-required' under 'disabled:' in .sloprail/config.yaml."
