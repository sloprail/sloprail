#!/usr/bin/env bash
# enter: a goal.yaml was written (create or update, settled). Read its
# `enabled`/`target` and activate only if the goal is currently in force.
# Receives ContextEnterPayload — event is a PostFileWrite-matched variant, so
# its content is the settled bytes, not a prediction.
set -uo pipefail

input="$(cat)"
content="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
goal_path="$(printf '%s' "$input" | jq -r '.event.path // ""')"

if [ -z "$content" ]; then
  exit 0
fi

enabled="$(printf '%s' "$content" | grep '^enabled:' | awk '{print $2}')"
goal_name="$(basename "$(dirname "$goal_path")")"

if [ "$enabled" != "true" ]; then
  # Written but not enabled — a goal can be authored and left off.
  exit 0
fi

jq -n --arg name "$goal_name" --arg path "$goal_path" \
  '{goal: $name, goal_path: $path}'
