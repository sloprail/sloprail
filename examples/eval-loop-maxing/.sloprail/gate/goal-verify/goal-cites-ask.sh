#!/usr/bin/env bash
# Stage 1 of goal-verify's grounding checks — deterministic, no model.
#
# The goal keeps its own condition alive across compaction (that is the whole
# point of goal.yaml living outside the agent's context window), but nothing
# stopped the goal from drifting from what the user actually asked: an agent
# could write `target: 0.75` and quietly drop the "don't hardcode the eval"
# half once the original prompt scrolled out of context. This script is the
# deterministic half of the fix — the goal must carry a `cited_ask:` citation
# of the user's own words, in the same `[quote](jsonl-path)` grammar
# sloprail-tasks's task-body-is-human-authored guard uses, and that quote must
# GROUND (via `sr-session trajectory cite`) to something the user actually
# said. Stage 2 (goal-covers-ask.md.j2) then judges whether goal.yaml's target
# + constraints actually preserve THAT quote's whole ask.
#
# `require: [{context: goal-tracking}]` on this gate guarantees goal.yaml
# exists and is enabled before this check ever runs.
set -uo pipefail

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

input="$(cat)"
goal_name="$(printf '%s' "$input" | jq -r '.context["goal-tracking"].payload.goal // empty' 2>/dev/null)"

if [ -z "$goal_name" ]; then
  # goal-tracking is not active — nothing to ground, permit (mirrors
  # run-verify.sh's own "nothing active" branch).
  exit 0
fi

gdir="${SR_GUARDRAIL_DIR:-.}"
lib="$gdir/ask-citation.sh"
if [ ! -f "$lib" ]; then
  refuse "goal-cites-ask: ask-citation.sh not found at $lib — the citation library this check needs is missing"
fi
# shellcheck source=./ask-citation.sh
. "$lib"

goal_dir="${SR_WORKSPACE:-.}/goal/$goal_name"
goal_yaml="$goal_dir/goal.yaml"

if [ ! -f "$goal_yaml" ]; then
  refuse "goal-cites-ask: goal '$goal_name' is active but $goal_yaml does not exist"
fi

content="$(cat "$goal_yaml")"

found="$(ask_citation_extract "$content")"
if [ -z "$found" ]; then
  refuse "goal-cites-ask: $goal_yaml carries no 'cited_ask: \"[quote](jsonl-path)\"' field. The goal must quote the user's own prompt so the target survives compaction without drifting from what was actually asked — e.g. cited_ask: \"[improve the classifier ... without hardcoding](/abs/path/session.jsonl:1)\"."
fi

href="$(printf '%s' "$found" | cut -f1)"
quote="$(printf '%s' "$found" | cut -f2-)"
cpath="$(ask_citation_href_path "$href")"
case "$cpath" in
  /*) : ;;
  *)  cpath="${SR_WORKSPACE:-.}/$cpath" ;;
esac

if ! reason="$(ask_citation_ground "$cpath" "$quote")"; then
  refuse "goal-cites-ask: $goal_yaml's cited_ask does not ground: $reason"
fi

exit 0
