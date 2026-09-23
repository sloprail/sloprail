#!/usr/bin/env bash
# enter: a goal.yaml was written (settled). Read its `enabled` and activate
# only if the goal is currently in force.
set -uo pipefail

input="$(cat)"
goal_path="$(printf '%s' "$input" | jq -r '.event.path // ""')"

# Content by event kind. Post runs in practice; the Pre branches keep the script
# correct for any kind. On a Pre write with resultKnown false, the content is not
# derivable yet — defer to the Post kind rather than mistake it for an empty file.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    content="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" != "true" ]; then
      # Result not derivable ahead of the write: defer to the Post kind.
      exit 0
    fi
    content="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  *)
    # No kind, or one this context is not about: nothing to activate on.
    exit 0
    ;;
esac

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
