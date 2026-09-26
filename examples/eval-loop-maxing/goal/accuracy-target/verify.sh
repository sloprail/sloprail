#!/usr/bin/env bash
# verify: is this goal's target met? Run by the goal-verify gate (via goal.yaml's
# `script:`) or by hand. Reads the latest value from the recording log, and the
# target itself from goal.yaml's own `target:` field, so the two never drift.
#
# NOTE ON TRUST: this script's exit code is NOT, by itself, what the gate's
# runner trusts to decide the Stop. run-verify.sh recomputes the metric
# independently and compares it against goal.yaml's `target:` on its own —
# specifically so an agent-written verify.sh that always `exit 0`s (or one
# that reads a value the agent controls) cannot pass the gate. This script
# still matters: it is what an agent runs by hand while iterating, and the
# convention every goal follows.
set -uo pipefail

TARGET_METRIC="accuracy"
TARGET_OP=">="

goal_dir="$(cd "$(dirname "$0")" && pwd)"
TARGET_THRESHOLD="$(grep '^target:' "$goal_dir/goal.yaml" | awk '{print $2}')"
if [ -z "$TARGET_THRESHOLD" ]; then
  echo "goal.yaml carries no 'target:' value to verify against" >&2
  exit 1
fi

record="${SR_WORKSPACE:-.}/evals/metrics.jsonl"

if [ ! -f "$record" ]; then
  echo "no recorded metric history at $record — nothing to verify against" >&2
  exit 1
fi

last="$(tail -1 "$record")"
value="$(printf '%s' "$last" | jq -r ".${TARGET_METRIC} // empty" 2>/dev/null)"

if [ -z "$value" ]; then
  echo "latest row in $record carries no '$TARGET_METRIC' value" >&2
  exit 1
fi

case "$TARGET_OP" in
  ">=") awk "BEGIN{exit !($value >= $TARGET_THRESHOLD)}" ;;
  "<=") awk "BEGIN{exit !($value <= $TARGET_THRESHOLD)}" ;;
  *)    echo "unsupported operator: $TARGET_OP" >&2; exit 1 ;;
esac
