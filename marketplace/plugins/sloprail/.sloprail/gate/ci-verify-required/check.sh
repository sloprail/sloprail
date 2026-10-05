#!/usr/bin/env bash
# Stop: a repository with file-guards must commit a CI job that runs `sr-checks verify`, marked by
# the sloprail marker `sr:ci verify` (written by `sr-mark apply ci --verify=<path>:<line>`, read by
# `sr-mark find`, the engine's own marker reader). Contract: stdin is the GateCheckPayload (unused: the
# decision is about the repository); exit 1 with {"reason": ...} refuses.
set -uo pipefail
cat >/dev/null

MARKER='sr:ci verify'

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
# A `.sloprail/` folder below the root counts too (marketplace/plugins/<p>/.sloprail/file-guard/): a file-guard
# anywhere in the repository is judged only by `sr-checks`, never at the write. A sr-test case folder is
# data, not a guard: its fixtures may be named like declarations (file-guard.yaml) without declaring anything,
# and may hold a whole nested `.sloprail/` of their own. So a `.sloprail/file-guard/` counts only when no
# `tests/` or `structure.tests/` folder lies above it, and it is not itself inside a rule's `tests/` or
# `structure.tests/` (<nature>/<rule>/tests/<case>/ at any depth, nested fixtures included).
all="$(git -C "$top" ls-tree -r --name-only HEAD 2>/dev/null)" || refuse "'git ls-tree' failed in $top, so whether the project has file-guards could not be checked"
own="$(printf '%s\n' "$all" | awk '
  {
    p = $0; off = 0
    while ((i = index(substr(p, off + 1), ".sloprail/file-guard/")) > 0) {
      pos = off + i
      if (pos == 1 || substr(p, pos - 1, 1) == "/") {
        pre = "/" substr(p, 1, pos - 1); rest = substr(p, pos + 21)
        if (pre !~ /\/(tests|structure\.tests)\// && rest !~ /^([^\/]+\/tests|structure\.tests)\//) { print; next }
      }
      off = pos + 20
    }
  }')" || own=""
fp="$(printf '%s' "$top" | { shasum 2>/dev/null || sha1sum 2>/dev/null || cksum; } | cut -d' ' -f1)"
[ -n "$fp" ] && key="repo:${fp}"
[ -n "$own" ] || { note no-guards; exit 0; }

command -v sr-mark >/dev/null 2>&1 || refuse "sr-mark is not on PATH, so whether the committed tree carries a '$MARKER' CI marker could not be checked. Install the sloprail plugin's binaries."
# `sr-mark find` reads the COMMITTED tree at HEAD with the engine's marker reader: 0 found, 1 none,
# anything else could not be read (a refusal, never a pass).
sr-mark find ci --fqn verify --rev HEAD --root "$top" >/dev/null 2>"${TMPDIR:-/tmp}/sr-ci-find.$$"
case $? in
  0) rm -f "${TMPDIR:-/tmp}/sr-ci-find.$$"; note marker-found; exit 0 ;;
  1) rm -f "${TMPDIR:-/tmp}/sr-ci-find.$$" ;;
  *) why="$(cat "${TMPDIR:-/tmp}/sr-ci-find.$$" 2>/dev/null)"; rm -f "${TMPDIR:-/tmp}/sr-ci-find.$$"
     refuse "'sr-mark find' failed in $top, so whether the committed tree carries a '$MARKER' CI marker could not be checked: $why" ;;
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

This project has its own file-guards under .sloprail/file-guard/, but no committed file carries the marker '$MARKER', so nothing shows that CI verifies their verdicts on pull requests. A file-guard's verdict is only enforced where 'sr-checks verify' runs: on this machine an agent can skip it, in CI it gates the merge.

Add a CI job that runs, on every pull request,
  sr-checks verify --base <the target branch (it uses the merge-base with the PR head)> --head <PR head sha>
(the PR's own head, not the provider's merge commit), put the marker '$MARKER' on its own comment line next to that step (write it with: sr-mark apply ci --verify=<path>:<line>), and COMMIT it: the check reads the committed tree, not your working copy. 'sr-checks verify' needs the history (a full clone); it fetches the verdicts that 'sr-checks run' stored on the sloprail/checks branch from origin itself and only reads them. Make the job a required status check.

Protect the default branch: require pull requests and up-to-date branches, and allow no direct pushes. Then the pull request's range is exactly what lands, and verify on pull requests is the guarantee; no job on pushes to the default branch is needed.

Install sr-checks with the release's install.sh, as below. The snippets pin ${tag}, the release installed here. Plugins the project enables in .claude/settings.json but CI has not installed (such as sloprail itself) are reported on stderr and their rules are NOT verified there; verify checks the project's own rules only, and its exit status is not affected by the missing plugins.

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
        - run: |
            ${install}
            echo \"\$HOME/.local/bin\" >> \"\$GITHUB_PATH\"
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
    script:
      - ${install}
      # sr:ci verify
      # CI_MERGE_REQUEST_DIFF_BASE_SHA is the merge-base of the target and the MR head; the MR head is CI_MERGE_REQUEST_SOURCE_BRANCH_SHA (in merged-results pipelines CI_COMMIT_SHA is a synthetic merge commit), falling back to CI_COMMIT_SHA
      - ~/.local/bin/sr-checks verify --base \"\$CI_MERGE_REQUEST_DIFF_BASE_SHA\" --head \"\${CI_MERGE_REQUEST_SOURCE_BRANCH_SHA:-\$CI_COMMIT_SHA}\"

Azure Pipelines (azure-pipelines.yml; add a build validation policy on the default branch):
  trigger: none
  pr: [main]
  steps:
    - checkout: self
      fetchDepth: 0
    - script: ${install}
    # sr:ci verify
    - script: ~/.local/bin/sr-checks verify --base origin/\$(System.PullRequest.TargetBranchName) --head \$(System.PullRequest.SourceCommitId)

Any other provider (Bitbucket Pipelines, Jenkins, CircleCI, ...): run the same 'sr-checks verify' on pull requests (--base the target branch, --head the PR head sha), with the marker line '$MARKER' in a comment beside it in the committed pipeline file. Jenkinsfile: '// $MARKER'. (The file-guard 'sloprail/file-guard/ci-verify-step' checks the marked file's pull request trigger for GitHub Actions, GitLab CI and Azure Pipelines; in any other file it requires only a line that runs 'sr-checks verify'.)

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
