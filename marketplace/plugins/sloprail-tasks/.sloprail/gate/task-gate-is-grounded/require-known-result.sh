#!/usr/bin/env bash
# First check of the task-gate-is-grounded gate: refuse a write whose result the
# engine could not compute ahead of the write.
#
# The gate's judge reads the pending bytes (`event.newContent`). When the engine
# could not derive them — a sed -i, a notebook create, an sr-file line it could not
# resolve — `resultKnown` is false and `newContent` is "" (indistinguishable from an
# emptied file), so a judge would be handed an empty gate and rule on nothing. A gate
# nobody saw is not approved: refuse, and say how to make the write derivable.
#
# THE REFUSAL CONTRACT: exit 0 permits; non-zero refuses with `{"reason": "..."}` on
# stdout.
set -uo pipefail

command -v jq >/dev/null 2>&1 || {
  echo "task-gate-is-grounded could not run: it needs jq, which is not on PATH. Refusing, because a check that could not run has not approved the write." >&2
  exit 1
}

payload="$(cat)"
if [ "$(printf '%s' "$payload" | jq -r '.event.resultKnown // false' 2>/dev/null)" != "true" ]; then
  path="$(printf '%s' "$payload" | jq -r '.event.path // ""' 2>/dev/null)"
  jq -n --arg path "$path" '{reason: ("task-gate-is-grounded: the result of this write to " + $path + " could not be computed ahead of the write (a sed -i, a notebook create, an unresolvable sr-file line), so the gate could not be judged. Write the file content directly (the Write tool), or run sr-file on its own line.")}'
  exit 1
fi
exit 0
