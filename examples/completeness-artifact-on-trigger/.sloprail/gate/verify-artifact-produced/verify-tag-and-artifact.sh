#!/usr/bin/env bash
# Reads the registry the paired context accumulated (sr-session state,
# owned by tag-declared): which tags were declared this cycle, and which
# artifact files actually landed. #skip needs no artifact; any other
# declared tag must have a matching artifact entry, or absence is the
# violation this rule exists to catch.
#
# Does NOT re-check whether a tag was declared at all — this gate's own
# `require: [{context: tag-declared}]` already guarantees the context
# activated (i.e. some tag showed up) before this script ever runs; the
# sibling tag-required gate is what catches the no-tag-at-all case
# (chicken-and-egg: tag-declared cannot activate on an absent tag).
set -uo pipefail

# 2026-08-20: read the tag-declared context's registry via `state list
# --owner tag-declared` (the read-only cross-guardrail read merged in
# b8608c3); the gate's own `require: [{context: tag-declared}]` guarantees
# that context entered THIS cycle first, so the entries are current. `state
# list` emits JSON-LINES, not an array, so both reads SLURP with `jq -s`
# before treating the stream as one — an earlier draft's `jq -r '[.[] | ...]'`
# on the raw lines read each object's field values instead of the stream,
# came back empty, and FALSE-REFUSED every legit tagged turn (an empty tag set
# is neither "skip" nor "has an artifact"). No cwd-relative path here to
# anchor on $SR_WORKSPACE — this gate reads only state.
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
