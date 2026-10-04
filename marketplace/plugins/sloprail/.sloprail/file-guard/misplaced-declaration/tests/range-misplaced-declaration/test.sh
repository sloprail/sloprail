#!/usr/bin/env bash
set -euo pipefail

# The CI path, no agent turn: `sr-checks run` judges a committed range with the plugin's file-guards.
git init -q .
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
BASE=$(git rev-parse HEAD)
# one agent run with no turn sets up the plugin (config dir and plugin cache) for sr-checks here
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
: > "$SR_EVENTS_FILE"

# a structure declaration one level too high is committed
mkdir -p .sloprail
printf 'allow:\n  - glob: ".sloprail/**"\n' > .sloprail/structure.yaml
git add .sloprail/structure.yaml
git -c user.name=t -c user.email=t@t commit -q -m "misplaced structure"
if sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1; then
  echo "the misplaced declaration passed sr-checks run" >&2
  exit 1
fi
jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="sloprail/misplaced-declaration" and .outcome=="refused" and (.reason|contains(".sloprail/file-guard/structure.yaml")))' "$SR_EVENTS_FILE" >/dev/null ||
  { jq -c . "$SR_EVENTS_FILE" >&2; echo "no refusal naming .sloprail/file-guard/structure.yaml" >&2; exit 1; }

# recovery: the same declaration where the engine reads it, over the same range from the same base, passes
: > "$SR_EVENTS_FILE"
git checkout -q -b corrected "$BASE"
mkdir -p .sloprail/file-guard
printf 'allow:\n  - glob: ".sloprail/**"\n' > .sloprail/file-guard/structure.yaml
git add .sloprail/file-guard/structure.yaml
git -c user.name=t -c user.email=t@t commit -q -m "structure, in file-guard/"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 ||
  { echo "the correctly placed declaration was refused" >&2; jq -c . "$SR_EVENTS_FILE" >&2; exit 1; }
jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="sloprail/misplaced-declaration" and .outcome=="passed") and (any(.[]; .kind=="FileGuardChecked" and .outcome=="refused")|not)' "$SR_EVENTS_FILE" >/dev/null ||
  { jq -c . "$SR_EVENTS_FILE" >&2; echo "misplaced-declaration did not pass the correctly placed file" >&2; exit 1; }
