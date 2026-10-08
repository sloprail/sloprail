# Contributing to sloprail

## Contributor License Agreement (CLA)

Every external pull request must be covered by a signed [Contributor
License Agreement](./CLA.md) before it can be merged. This protects both
you and the project: it confirms you have the right to contribute the code
and gives the project a clear, durable license to use it.

### How it works

1. Open your pull request as usual.
2. A bot ("CLA Assistant") comments on the PR and posts a status check.
   - If you (or your GitHub username) have already signed, the check passes
     immediately and no further action is needed.
   - If you haven't signed yet, the bot asks you to do so.
3. To sign, read [CLA.md](./CLA.md), then post **exactly** this sentence as
   a new comment on your pull request:

   ```
   I have read the CLA Document and I hereby sign the CLA
   ```

4. The bot records your signature (GitHub username + PR number) in a JSON
   file on the dedicated `cla-signatures` branch of this repository — not on
   `main` — and re-runs the check, which should now pass.
5. If the check doesn't automatically re-run, comment `recheck` on the PR to
   trigger it manually.
6. You only need to sign once. Future pull requests from the same GitHub
   account are automatically recognized.

### Notes

- The CLA check is a required status check; pull requests cannot be merged
  until it passes (see repository branch protection settings).
- Organization owners and known bot accounts (Dependabot, Renovate,
  GitHub Actions, etc.) are allowlisted and skip the CLA check — see the
  `allowlist` input in [`.github/workflows/cla.yml`](./.github/workflows/cla.yml).
- If your commits are authored by someone other than you (e.g. you're
  submitting a patch on behalf of someone else, or doing release-engineering
  cherry-picks), see the `require-opener-as-author` note in the workflow
  file — by default the bot expects the PR opener to be an author or
  co-author of at least one commit in the PR, to prevent someone from
  opening a PR against commits they don't control.
- Questions about the CLA itself (not the bot mechanics) should go to
  A10N, Inc. directly — see [CLA.md](./CLA.md) for the current
  legal-review status of that document.

## Plugin files come from the release tag

`.claude-plugin/marketplace.json` lists each plugin with a `git-subdir`
source pinned to a release tag (`"ref": "v0.3.0"`), so users who add the
marketplace get the plugin files (hooks, wrapper, skills) from the same tag as
the released binaries, and `main` can hold unreleased work. `make cut-release`
(via `scripts/bump-version.sh`) rewrites the `version` and the `ref` together,
and `make verify-version` refuses a tag whose ref disagrees.

The pin means `/plugin marketplace add <local checkout>` does NOT test your
working tree: the marketplace file is read locally but each plugin is still
fetched from the tag. To try unreleased plugin changes, load the directory
for one session instead:

```
claude --plugin-dir marketplace/plugins/sloprail
```

## Running the tests

`make test-unit` (unit and `tests/repo`), `make test-services`, and
`make test-e2e` (the e2e suites, which drive a mock agent). Tests never skip
because of the environment: a missing prerequisite fails the test with the fix.

Install these first:

- Go (the version in `go.mod`), `git`, and `sh`.
- `jq` and `yq` (mikefarah's) on `PATH`: `brew install jq yq`, or
  `apt-get install jq` plus the `yq` binary from
  <https://github.com/mikefarah/yq/releases>. CI installs both, and
  `./install.sh --tools-only` installs whichever is missing.
- The pinned mock agents: `make mock` installs the versions pinned in
  `tests/e2e/harness/{MOCK,CODEX_MOCK,CURSOR_MOCK}_VERSION` into `.bin/`. The
  e2e harness runs the same install itself, once per test binary, when the
  pinned mock is missing, and fails with the reason if it cannot.
