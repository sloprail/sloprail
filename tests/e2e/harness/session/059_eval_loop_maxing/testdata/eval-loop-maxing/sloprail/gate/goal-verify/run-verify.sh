#!/usr/bin/env bash
# Runs verify and decides the Stop. `require` guarantees goal-tracking ran first,
# so just read what it left behind.
set -uo pipefail

input="$(cat)"
goal_name="$(printf '%s' "$input" | jq -r '.context["goal-tracking"].payload.goal // empty' 2>/dev/null)"

# GateCheckPayload carries `context` at top level.
if [ -z "$goal_name" ]; then
  # goal-tracking is not active — nothing to verify, permit the Stop.
  exit 0
fi

ws="$(cd "${SR_WORKSPACE:-.}" && pwd)" || exit 1
goal_dir="$ws/goal/$goal_name"
script_name="$(grep '^script:' "$goal_dir/goal.yaml" | awk '{print $2}')"
verify_script="$goal_dir/${script_name:-verify.sh}"

if [ ! -x "$verify_script" ]; then
  echo "goal '$goal_name' is active but its verify script is missing or not executable at $verify_script" >&2
  exit 1
fi

# A check runs with the guard folder as cwd; verify.sh is written against
# repo-relative paths (evals/metrics.jsonl), so run it from the workspace root.
cd "$ws" || {
  echo "cannot enter the workspace $ws to run the verify script" >&2
  exit 1
}

if "$verify_script"; then
  # Target met — permit the Stop.
  exit 0
fi

echo "Goal '$goal_name' target not yet met. Do not stop: keep iterating until verify.sh passes." >&2
exit 1
