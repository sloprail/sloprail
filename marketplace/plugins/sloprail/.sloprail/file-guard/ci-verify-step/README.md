# ci-verify-step

Checks that a committed file marked `sr:ci verify` really is the CI job the marker promises.

`sloprail/gate/ci-verify-required` finds the marker with `sr-mark find ci --fqn verify`; it cannot
tell whether the file behind it does anything. This file-guard (a deterministic script, no model)
reads each changed file that carries `sr:ci verify` and requires, comments ignored:

- a step that runs `sr-checks verify`;
- GitHub Actions (`.github/workflows/*.yml`): a `pull_request` trigger under `on:`;
- GitLab CI (`.gitlab-ci.yml`): a `rules: - if:` line for `merge_request_event`;
- Azure Pipelines (`azure-pipelines*.yml`): a top-level `pr:`;
- any other file (Jenkinsfile, Bitbucket, CircleCI, ...): only the `sr-checks verify` step; its triggers are not checked. A push trigger is allowed everywhere but never required.

Write the marker with `sr-mark apply ci --verify=<path>:<line>`. Turn the rule off by listing
`sloprail/file-guard/ci-verify-step` under `disabled:` in `.sloprail/config.yaml`.
