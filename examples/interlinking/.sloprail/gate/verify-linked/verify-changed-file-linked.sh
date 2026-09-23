#!/usr/bin/env bash
# For every CREATED person this cycle: at least one link from elsewhere
# (updates/decisions) must exist. For every DELETED person: no dangling links
# to them should remain. Reads the full registry the paired context's enter
# accumulated — every file touched this cycle, not just one.
#
# Reads the people-linked context's registry via `state list --owner
# people-linked`; the gate's own `require: [{context: people-linked}]`
# guarantees that context entered THIS cycle first, so the entries are current.
# Two things to watch:
#   - `state list` emits JSON-LINES (one {key,value} per line), not a JSON
#     array, so any jq treating the stream as an array must slurp it with `-s`.
#   - the link greps run with cwd = this guardrail's own folder, not the repo
#     root, so a bare `updates/ decisions/` would look in the wrong tree — they
#     are anchored on $SR_WORKSPACE (the tree being guarded), with `.` fallback.
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
