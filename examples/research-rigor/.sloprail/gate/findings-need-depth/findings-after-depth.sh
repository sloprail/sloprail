#!/usr/bin/env bash
# Before a research-notes write: if a #research run is open, the write waits for
# depth. Depth itself is judged by depth-check's verify-depth.sh — one rule,
# shared, not a copy that could drift.
set -uo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
input="$(cat)"
path="$(printf '%s' "$input" | jq -r '.event.path // empty')"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath // empty')"

block() {
  echo "$1" >&2
  exit 1
}

[ -n "$transcript_path" ] || block "Whether a #research run is open could not be read: the check got no transcript path, so writing $path is held."

# Is a #research run open?
#   - the research-run context is active (declared in an earlier turn, or by a
#     #research dispatch), or
#   - the record already declares #research: a tag in the agent's own text, a
#     sub-agent dispatch whose prompt carries it, or — in a sub-agent — the
#     prompt it was dispatched with. A tag written earlier in THIS turn is on
#     the record before this write's tool call, but the context only hears of
#     it at Stop, which is too late for a write that must not land first.
open="$(printf '%s' "$input" | jq -r '.context["research-run"].active // false')"
if [ "$open" != "true" ]; then
  if ! entries="$(sr-session trajectory normalize --path "$transcript_path" --events PostTagWrite 2>&1)"; then
    block "Whether a #research run is open could not be read from $transcript_path, so writing $path is held: $entries"
  fi
  if ! declared="$(printf '%s' "$entries" | jq -r '
    [ .[]
      | ( (.events[]? | select(.kind == "PostTagWrite") | .tags[]? | select(.label == "research"))
        , (select(.type == "assistant") | .message.content[]? | select(.type == "tool_use")
           | select(.name == "Agent" or .name == "Task")
           | select((.input.prompt // "") | contains("#research")))
        , (select(.type == "user" and .isSidechain == true)
           | .message.content | strings | select(contains("#research"))) )
    ] | length > 0' 2>&1)"; then
    block "Whether a #research run is open could not be decided from $transcript_path, so writing $path is held: $declared"
  fi
  case "$declared" in
    false) exit 0 ;;   # no research declared: an ordinary write
    true) ;;
    *) block "Whether a #research run is open could not be decided from $transcript_path (got '$declared'), so writing $path is held." ;;
  esac
fi

# Research is open: the write lands only once the run has depth.
printf '%s' "$input" | DEPTH_FOR_WRITE="$path" bash "$here/../depth-check/verify-depth.sh"
