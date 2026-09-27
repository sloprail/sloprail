#!/usr/bin/env bash
# prepare for goal-covers-ask.md.j2: hands the judge the resolved cited_ask
# quote (the ground truth) and the goal's own declared target/constraints, so
# the template never has to parse goal.yaml or the transcript itself.
#
# Reached only once goal-cites-ask.sh passed, so a cited_ask citation is known
# to exist and to ground. This prepare's job is just to assemble the goal's
# own fields as the judge's material.
set -uo pipefail

input="$(cat)"
goal_name="$(printf '%s' "$input" | jq -r '.context["goal-tracking"].payload.goal // empty' 2>/dev/null)"

if [ -z "$goal_name" ]; then
  jq -n '{additionalContext: {ok: false, goal_yaml: "", cited_quote: ""}}'
  exit 0
fi

gdir="${SR_GUARDRAIL_DIR:-.}"
lib="$gdir/ask-citation.sh"
# shellcheck source=./ask-citation.sh
. "$lib"

goal_dir="${SR_WORKSPACE:-.}/goal/$goal_name"
goal_yaml_content="$(cat "$goal_dir/goal.yaml" 2>/dev/null || true)"

found="$(ask_citation_extract "$goal_yaml_content")"
quote="$(printf '%s' "$found" | cut -f2-)"

jq -n --arg ok "true" --arg y "$goal_yaml_content" --arg q "$quote" \
  '{additionalContext: {ok: ($ok == "true"), goal_yaml: $y, cited_quote: $q}}'
