#!/usr/bin/env bash
# enter: a goal.yaml was written (create or update, settled). Read its
# `enabled`/`target` and activate only if the goal is currently in force.
# Receives ContextEnterPayload — event is a PostFileWrite-matched variant, so
# its content is the settled bytes, not a prediction.
set -uo pipefail

input="$(cat)"
goal_path="$(printf '%s' "$input" | jq -r '.event.path // ""')"

# Where the bytes come from, chosen by event kind — the honest handling of
# `newContent`, not a bare `.event.newContent`. This context is bound to Post
# file events (its content is settled), so in practice it is the Post branch
# that runs; the Pre branches are here so the script is correct for whatever
# kind it is handed. On a Pre write, `.event.resultKnown` is the flag: it can be
# false on BOTH a create AND an update (a command-derived edit, or a NotebookEdit
# creating a fresh .ipynb whose cell source is not the document), and an absent
# `newContent` there must NOT be mistaken for an emptied file — the honest move
# is to DEFER to the Post kind, which carries the settled bytes.
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
