# Deploy Runbook

## Prerequisites

- VPN access to the staging network
- `kubectl` context set to `staging-cluster`

## Steps

1. Run `./scripts/build.sh` to produce the release artifact.
2. Run `./scripts/deploy.sh staging` to push it.
3. Watch the rollout with `kubectl rollout status deploy/api`.

## Rollback

If the rollout fails, run `./scripts/rollback.sh staging` to revert to the
previous release immediately.
