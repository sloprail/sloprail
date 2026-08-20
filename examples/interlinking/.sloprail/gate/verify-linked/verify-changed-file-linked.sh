#!/usr/bin/env bash
# For every CREATED person this cycle: at least one link from elsewhere
# (updates/decisions) must exist. For every DELETED person: no dangling
# links to them should remain. Reads the full registry the paired context's
# enter+watch phases accumulated — not just one file, however many were
# touched this cycle.
set -uo pipefail

entries="$(sr-session state list --owner people-linked 2>/dev/null)"

if [ -z "$entries" ] || [ "$(printf '%s' "$entries" | jq 'length')" -eq 0 ]; then
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
      if ! grep -rlq "$name" updates/ decisions/ 2>/dev/null; then
        failures="$failures $path(unlinked)"
      fi
      ;;
    PostFileDelete)
      if grep -rlq "$name" updates/ decisions/ 2>/dev/null; then
        failures="$failures $path(still-referenced)"
      fi
      ;;
  esac
done < <(printf '%s' "$entries" | jq -c '.[]')

if [ -n "$failures" ]; then
  echo "Interlinking check failed for:$failures" >&2
  exit 1
fi

exit 0
