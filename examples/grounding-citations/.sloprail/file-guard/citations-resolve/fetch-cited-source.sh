#!/usr/bin/env bash
# prepare: the script already confirmed every citation link resolves — pull
# the actual cited text out of each source here, once, so the judge template
# never has to parse a citation link itself.
#
# Output must nest under additionalContext (2026-08-19, his correction, PR
# #2 review 4974594141: only that one key is read from prepare's stdout,
# merged alongside the standard payload — never in place of it).
set -uo pipefail

input="$(cat)"

# Bytes to prepare from, chosen by event kind — the honest three-case handling
# of `newContent`, not the two-case `has("newContent")` short-cut that reads an
# absent field as "". Mirrors citation-links-resolve.sh, which validated the
# same content moments earlier.
#
# This guard is an AFTER-check (file-guard.yaml declares no `preventive:`), so at
# runtime it only fires on the settled POST event, where the content is always
# present. The Pre branches are here so the script is correct for whatever kind
# it is handed: `.event.resultKnown` distinguishes "the update empties the file"
# from "the result was not derivable", and on an underivable PreFileUpdate we
# DEFER to the Post kind (fires at Stop on the settled file) — emitting the empty
# additionalContext prepare must always emit — rather than guessing at absent
# content. See .sloprail/file-guard/skill-quality/judge-skill.sh for the idiom
# and the authoring-slop rule content-may-be-unresolvable for why.
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
new=""
case "$kind" in
  PostFileCreate|PostFileUpdate)
    new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    ;;
  PreFileCreate|PreFileUpdate)
    # resultKnown can be false on a create AS WELL AS an update (a NotebookEdit
    # fresh .ipynb PreFileCreate, or a command-derived edit) — so gate BOTH on it.
    known="$(printf '%s' "$input" | jq -r '.event.resultKnown // false')"
    if [ "$known" = "true" ]; then
      new="$(printf '%s' "$input" | jq -r '.event.newContent // ""')"
    fi
    # known != true → leave $new empty; the empty-context branch below fires and
    # the Post kind prepares the settled content at Stop.
    ;;
esac

if [ -z "$new" ]; then
  jq -n '{additionalContext: {citations: []}}'
  exit 0
fi

# Same shape citation-links-resolve.sh validated: [text](/abs/path:start-end).
matches="$(printf '%s' "$new" | grep -oE '\[[^]]*\]\(/[^)]+:[0-9]+-[0-9]+\)')"

pairs="[]"
while IFS= read -r m; do
  [ -z "$m" ] && continue
  quote="$(printf '%s' "$m" | sed -E 's/^\[([^]]*)\].*/\1/')"
  ref="$(printf '%s' "$m" | sed -E 's/^\[[^]]*\]\((.*)\)$/\1/')"
  file="${ref%:*}"
  range="${ref##*:}"
  start="${range%-*}"
  end="${range#*-}"

  source_text="$(sed -n "${start},${end}p" "$file" 2>/dev/null)"

  pairs="$(printf '%s' "$pairs" | jq --arg q "$quote" --arg r "$ref" --arg s "$source_text" \
    '. + [{quote: $q, reference: $r, source_text: $s}]')"
done <<< "$matches"

jq -n --argjson c "$pairs" '{additionalContext: {citations: $c}}'
