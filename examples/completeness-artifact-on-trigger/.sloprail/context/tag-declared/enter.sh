#!/usr/bin/env bash
# enter: logs whichever fired (tag or artifact touch) into the registry
# (sr-session state) rather than into `payload`, so a tag and an artifact landing
# in different tool calls both get recorded.
set -uo pipefail

input="$(cat)"
kind="$(printf '%s' "$input" | jq -r '.event.kind // ""')"

case "$kind" in
  PostTagWrite)
    tags="$(printf '%s' "$input" | jq -r '.event.tags[]?.label // empty')"
    while IFS= read -r tag; do
      [ -z "$tag" ] && continue
      sr-session state set "tag:$tag" "declared"
    done <<< "$tags"
    ;;
  PostFileCreate|PostFileUpdate)
    path="$(printf '%s' "$input" | jq -r '.event.path // ""')"
    [ -n "$path" ] && sr-session state set "artifact:$path" "$kind"
    ;;
esac

jq -n '{active_since: "trajectory"}'
