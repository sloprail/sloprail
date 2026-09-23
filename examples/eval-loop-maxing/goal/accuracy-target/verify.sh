#!/usr/bin/env bash
# verify: is this goal's target met? Run by the goal-verify gate (via goal.yaml's
# `script:`) or by hand. The target lives here; reads the latest value from the
# recording log.
set -uo pipefail

TARGET_METRIC="accuracy"
TARGET_OP=">="
TARGET_THRESHOLD="0.95"

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
