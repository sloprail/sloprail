#!/usr/bin/env bash
# Reads the registry the paired context accumulated (sr-session state,
# owned by tag-declared): which tags were declared this cycle, and which
# artifact files actually landed. #skip needs no artifact; any other
# declared tag must have a matching artifact entry, or absence is the
# violation this rule exists to catch.
set -uo pipefail

entries="$(sr-session state list --owner tag-declared 2>/dev/null)"

tags="$(printf '%s' "$entries" | jq -r '[.[] | select(.key | startswith("tag:"))] | .[].key | ltrimstr("tag:")')"
artifacts="$(printf '%s' "$entries" | jq -r '[.[] | select(.key | startswith("artifact:"))] | length')"

if [ -z "$tags" ]; then
  echo "This turn declared no tag (#update, #decision, or #skip) — declare one and produce the artifact it demands." >&2
  exit 1
fi

if echo "$tags" | grep -qx "skip"; then
  exit 0
fi

if [ "${artifacts:-0}" -eq 0 ]; then
  echo "Turn declared a tag ($tags) but no matching artifact was produced this turn — the tag was stated, the artifact was not." >&2
  exit 1
fi

exit 0
