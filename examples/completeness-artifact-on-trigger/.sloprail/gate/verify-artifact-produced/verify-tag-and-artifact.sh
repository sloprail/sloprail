#!/usr/bin/env bash
# Reads tag-declared's registry: which tags were declared, and how many artifacts
# landed. #skip needs no artifact; any other tag must have a matching artifact.
# The no-tag-at-all case is the sibling tag-required gate's job, not this one's.
#
# `require: [{context: tag-declared}]` guarantees the context ran first, so the
# entries are current. `state list` emits JSON-LINES, so reads slurp with `jq -s`.
set -uo pipefail

entries="$(sr-session state list --owner tag-declared 2>/dev/null)"

tags="$(printf '%s' "$entries" | jq -s -r '[.[] | select(.key | startswith("tag:"))] | .[].key | ltrimstr("tag:")')"
artifacts="$(printf '%s' "$entries" | jq -s -r '[.[] | select(.key | startswith("artifact:"))] | length')"

if echo "$tags" | grep -qx "skip"; then
  exit 0
fi

if [ "${artifacts:-0}" -eq 0 ]; then
  echo "Turn declared a tag ($tags) but no matching artifact was produced this turn — the tag was stated, the artifact was not." >&2
  exit 1
fi

exit 0
