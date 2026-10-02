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

command -v sr-checks >/dev/null 2>&1 ||
  refuse "sr-checks is not on PATH, so the file-guards this project loads could not be listed to tell whether CI must verify them. Install the sloprail plugin's binaries."

# The config at the default-branch base is what loading honours, as for `sr-checks verify`; a
# repository with no default branch answers git's empty tree, which --base cannot take: the root
# commit is the widest base it accepts.
base="$(sr-checks default-base --head HEAD 2>/dev/null)" || refuse "ci-verify-required could not find the default branch's base for HEAD, so the file-guards could not be listed"
if [ "$base" = "4b825dc642cb6eb9a060e54bf8d69288fbee4904" ]; then
  base="$(git rev-list --max-parents=0 --reverse --date-order HEAD 2>/dev/null | head -n 1)"
fi
[ -n "$base" ] || refuse "ci-verify-required could not resolve a base for HEAD, so the file-guards could not be listed"

if ! guards="$(sr-checks guards --base "$base" --head HEAD 2>/dev/null)"; then
  refuse "'sr-checks guards --base $base --head HEAD' failed, so the file-guards this project loads could not be listed. Run it to see why."
fi
[ -n "$guards" ] || exit 0

git grep -q -F -e "$MARKER" HEAD -- 2>/dev/null
case $? in
  0) exit 0 ;;
  1) ;;
  *) refuse "'git grep' failed in ${SR_WORKSPACE:-.}, so whether the committed tree carries a '$MARKER' CI marker could not be checked" ;;
esac

n="$(printf '%s\n' "$guards" | wc -l | tr -d ' ')"
refuse "This project loads $n file-guard(s), but no committed file contains '$MARKER', so nothing shows that CI verifies their verdicts on pull requests. A file-guard's verdict is only enforced where 'sr-checks verify' runs: on this machine an agent can skip it, in CI it gates the merge.

Add a CI job that runs, on every pull request,
  sr-checks verify --base <default branch> --head <pull request head sha>
(the PR's own head, not the provider's merge commit), put the comment '$MARKER' next to that step, and COMMIT it: the check reads the committed tree, not your working copy. 'sr-checks verify' needs the history (a full clone) and reads the verdicts that 'sr-checks run' stored on the sloprail/checks branch.

GitHub Actions (.github/workflows/sloprail.yml):
  on: pull_request
  jobs:
    sloprail-verify:
      runs-on: ubuntu-latest
      steps:
        - uses: actions/checkout@v4
          with:
            fetch-depth: 0
            ref: \${{ github.event.pull_request.head.sha }}
        - run: curl -fsSL https://raw.githubusercontent.com/sloprail/sloprail/main/install.sh | sh
        # sr-mark: ci-verify
        - run: ~/.local/bin/sr-checks verify --base origin/\${{ github.event.pull_request.base.ref }} --head \${{ github.event.pull_request.head.sha }}

GitLab CI (.gitlab-ci.yml):
  sloprail-verify:
    rules:
      - if: \$CI_PIPELINE_SOURCE == \"merge_request_event\"
    variables:
      GIT_DEPTH: \"0\"
    script:
      - curl -fsSL https://raw.githubusercontent.com/sloprail/sloprail/main/install.sh | sh
      # sr-mark: ci-verify
      - ~/.local/bin/sr-checks verify --base origin/\$CI_MERGE_REQUEST_TARGET_BRANCH_NAME --head \$CI_MERGE_REQUEST_SOURCE_BRANCH_SHA

Azure Pipelines (azure-pipelines.yml; add a build validation policy on the default branch):
  pr: [main]
  steps:
    - checkout: self
      fetchDepth: 0
    - script: curl -fsSL https://raw.githubusercontent.com/sloprail/sloprail/main/install.sh | sh
    # sr-mark: ci-verify
    - script: ~/.local/bin/sr-checks verify --base origin/\$(System.PullRequest.TargetBranchName) --head \$(System.PullRequest.SourceCommitId)

Any other provider (Bitbucket Pipelines, Jenkins, CircleCI, ...): run the same 'sr-checks verify --base <default branch> --head <PR head sha>' as a required check on pull requests, with the line '$MARKER' in a comment beside it in the committed pipeline file. Jenkinsfile: '// $MARKER'.

To turn this off, list 'sloprail/gate/ci-verify-required' under 'disabled:' in .sloprail/config.yaml."
