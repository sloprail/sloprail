#!/usr/bin/env bash
# For each CREATED person this cycle: at least one link from updates/decisions
# must exist. For each DELETED person: no dangling links may remain. Reads the
# full registry via `state list --owner people-linked` (the gate's require keeps
# the entries current). Two gotchas:
#   - `state list` emits JSON-LINES, not an array — slurp with jq `-s`.
#   - greps are anchored on $SR_WORKSPACE (with `.` fallback) because cwd is this
#     guardrail's own folder, not the repo root.
set -uo pipefail

ws="${SR_WORKSPACE:-.}"

entries="$(sr-session state list --owner people-linked 2>/dev/null)"

if [ -z "$entries" ] || [ "$(printf '%s' "$entries" | jq -s 'length')" -eq 0 ]; then
  exit 0
fi

failures=""
while IFS= read -r row; do
  [ -z "$row" ] && continue
  path="$(printf '%s' "$row" | jq -r '.key')"
  kind="$(printf '%s' "$row" | jq -r '.value')"
  name="$(basename "$path" .md)"

  case "$kind" in
    PostFileCreate)
      if ! grep -rlq "$name" "$ws/updates" "$ws/decisions" 2>/dev/null; then
        failures="$failures $path(unlinked)"
      fi
      ;;
    PostFileDelete)
      if grep -rlq "$name" "$ws/updates" "$ws/decisions" 2>/dev/null; then
        failures="$failures $path(still-referenced)"
      fi
      ;;
  esac
done < <(printf '%s' "$entries" | jq -s -c '.[]')

if [ -n "$failures" ]; then
  echo "Interlinking check failed for:$failures" >&2
  exit 1
fi

exit 0
