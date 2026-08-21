#!/usr/bin/env bash
# Cheap first cut: every citation link in the new content must resolve to a
# real chunk of the trajectory/source — the link exists, before any judge
# is asked whether the quote actually matches it.
set -uo pipefail

input="$(cat)"

# Where the bytes to check come from, chosen by event kind — the honest
# three-case handling of `newContent`, not the two-case `has("newContent")`
# short-cut that reads an absent field as "".
#
# This guard is an AFTER-check (its file-guard.yaml declares no `preventive:`),
# so at runtime it only ever fires on the settled POST event, where the content
# is always present. The Pre branches below are not reached in this guard's
# binding — they are here so the script is correct for whatever kind it is
# handed, and so `resultKnown` is consulted rather than an absent `newContent`
# being mistaken for an emptied file. `.event.resultKnown` is the flag that
# tells "the update empties the file" from "the result was not derivable"; on an
# underivable PreFileUpdate the honest move is to DEFER to the Post kind (which
# fires at Stop on the settled file) rather than guess. See the sibling
# .sloprail/file-guard/skill-quality/judge-skill.sh for the same idiom, and the
# authoring-slop rule content-may-be-unresolvable for why.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
case "$kind" in
  PreFileCreate|PostFileCreate|PostFileUpdate)
    # Create carries the new body; Post carries the settled body. Always present.
    new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  PreFileUpdate)
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" != "true" ]; then
      # Result not derivable ahead of the write: defer to the Post kind, which
      # judges what actually landed. Not a permit-by-ignorance — the settled
      # content is checked at Stop.
      exit 0
    fi
    new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  *)
    # No kind, or a kind this guard is not about (e.g. a delete): nothing to check.
    exit 0
    ;;
esac

if [ -z "$new" ]; then
  exit 0
fi

# Citations are written as [text](/abs/path:start-end) — an absolute path,
# not a bare "jsonl:" tag (2026-08-19, his catch: a session can have MULTIPLE
# jsonl files over a file's lifetime, so a citation must pin WHICH transcript
# file it points into, not assume "the" one). A trajectory citation and a
# source-file citation are the same shape; only the path differs.
citations="$(printf '%s' "$new" | grep -oE '\]\((/[^)]+:[0-9]+-[0-9]+)\)' | sed 's/^](//; s/)$//')"

if [ -z "$citations" ]; then
  exit 0
fi

unresolved=""
while IFS= read -r ref; do
  [ -z "$ref" ] && continue
  file="${ref%:*}"
  range="${ref##*:}"
  start="${range%-*}"
  end="${range#*-}"

  if [ ! -f "$file" ]; then
    unresolved="$unresolved $ref"
    continue
  fi

  if ! sed -n "${start},${end}p" "$file" > /dev/null 2>&1; then
    unresolved="$unresolved $ref"
  fi
done <<< "$citations"

if [ -n "$unresolved" ]; then
  echo "These citations do not resolve to a real source:$unresolved" >&2
  exit 1
fi

exit 0
