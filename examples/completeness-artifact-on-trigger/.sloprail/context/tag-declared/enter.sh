#!/usr/bin/env bash
# enter: runs on every occurrence of either trigger — a tag being written
# or an artifact file being touched. Logs whichever happened into the
# registry (sr-session state, keyed on this context's own name) rather than
# growing `payload` in place, so a turn where the tag and the artifact land
# in different tool calls still ends up with both recorded.
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
