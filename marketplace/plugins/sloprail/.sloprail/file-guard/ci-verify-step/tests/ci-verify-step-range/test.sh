#!/usr/bin/env bash
set -euo pipefail

# The CI path, no agent turn: a workflow carrying `sr:ci verify` promises CI runs `sr-checks verify` on pull
# requests. `sr-checks run` refuses what lacks either half, naming exactly what is missing, and passes the
# complete one. Each variant is its own commit on a branch from the same base.
git init -q -b main .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
BASE=$(git rev-parse HEAD)
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
fail() { echo "$1" >&2; jq -c . "$SR_EVENTS_FILE" >&2; exit 1; }

variant() { # name workflow-body : commit it on its own branch from BASE, leave HEAD there
  git checkout -q -b "$1" "$BASE"
  mkdir -p .github/workflows
  printf '%s\n' "$2" > .github/workflows/sloprail.yml
  git add .github/workflows/sloprail.yml
  git -c user.name=t -c user.email=t@t commit -q -m "$1"
}
refused_with() { # branch substring...: the range is refused and the ci-verify-step reason holds every substring
  : > "$SR_EVENTS_FILE"
  if sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1; then fail "$1: sr-checks run passed a workflow that breaks the promise"; fi
  local b=$1; shift
  for s in "$@"; do
    jq -es --arg s "$s" 'any(.[]; .kind=="FileGuardChecked" and .rule=="sloprail/ci-verify-step" and .outcome=="refused" and (.reason|contains($s)))' "$SR_EVENTS_FILE" >/dev/null || fail "$b: the refusal does not say: $s"
  done
}

# 1. the marker, and a step that is only commented out, on a push trigger: both halves missing
variant nothing $'# sr:ci verify\non:\n  push:\njobs:\n  v:\n    runs-on: ubuntu-latest\n    steps:\n      # - run: sr-checks verify --base main --head HEAD\n      - run: echo hi'
refused_with nothing "a step that runs 'sr-checks verify'" "a 'pull_request' trigger"

# 2. the step is there, the trigger is not: only the trigger is named
variant no-trigger $'# sr:ci verify\non:\n  push:\njobs:\n  v:\n    runs-on: ubuntu-latest\n    steps:\n      - run: sr-checks verify --base main --head HEAD'
refused_with no-trigger "a 'pull_request' trigger"
jq -es 'all(.[]|select(.rule=="sloprail/ci-verify-step" and .outcome=="refused"); (.reason|contains("a step that runs")|not))' "$SR_EVENTS_FILE" >/dev/null || fail "no-trigger: the refusal also names the step, which is there"

# 3. the fix, committed on top of the refused branch: the trigger is added and nothing else changes
mkdir -p .github/workflows
printf '%s\n' $'# sr:ci verify\non:\n  pull_request:\njobs:\n  v:\n    runs-on: ubuntu-latest\n    steps:\n      - run: sr-checks verify --base main --head HEAD' > .github/workflows/sloprail.yml
git add .github/workflows/sloprail.yml
git -c user.name=t -c user.email=t@t commit -q -m "add the pull_request trigger"
: > "$SR_EVENTS_FILE"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 || fail "fixed: the workflow that keeps the promise was refused"
jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="sloprail/ci-verify-step" and .outcome=="passed") and (any(.[]; .kind=="FileGuardChecked" and .outcome=="refused")|not)' "$SR_EVENTS_FILE" >/dev/null || fail "fixed: ci-verify-step did not pass it"
