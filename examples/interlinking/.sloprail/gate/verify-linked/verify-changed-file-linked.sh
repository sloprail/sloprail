#!/usr/bin/env bash
# For every CREATED person this cycle: at least one link from elsewhere
# (updates/decisions) must exist. For every DELETED person: no dangling
# links to them should remain. Reads the full registry the paired context's
# enter accumulated — not just one file, however many were touched this
# cycle.
#
# 2026-08-20: reads the people-linked context's registry via `state list
# --owner people-linked` (the read-only cross-guardrail read merged in
# b8608c3). The gate's own `require: [{context: people-linked}]` guarantees
# that context entered THIS cycle before this read, so the entries are
# current. Two fixes went in with that read:
#   - `state list` emits JSON-LINES (one {key,value} object per line), not a
#     JSON array, so every jq that treats the stream as an array must slurp
#     it first with `-s`. An earlier draft piped the lines through `jq '.[]'`
#     / `jq 'length'`, which iterate a single object's field values rather
#     than the stream — it read nothing and the gate passed everything.
#   - the link greps run with cwd = this guardrail's own folder, NOT the repo
#     root, so a bare `updates/ decisions/` looked in the wrong tree. They are
#     anchored on $SR_WORKSPACE (the tree being guarded), matching the working
#     eval-loop-maxing example's `${SR_WORKSPACE:-.}` pattern; the fallback to
#     `.` keeps it runnable if the variable is somehow unset.
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
