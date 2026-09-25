#!/usr/bin/env bash
# Assembles what the judge needs to reason about paraphrased hints —
# no-hints.sh's own literal grep only catches a fixture's exact rule/skill
# names, and cannot tell "load the credential-tracking skill first" is just
# as much a hint as "load update-credential-inventory first". The judge
# needs the actual gate/skill bodies to recognize a paraphrase, not just
# their directory names.
set -uo pipefail

payload="$(cat)"
path=$(printf '%s' "$payload" | jq -r '.event.path')

if [ -z "${SR_WORKSPACE:-}" ]; then
  echo "eval-prompt-no-hints prepare: SR_WORKSPACE is not set — cannot locate the fixture this prompt belongs to" >&2
  exit 1
fi

fixture_dir="$SR_WORKSPACE/$(dirname "$path")"
fixture_yaml="$fixture_dir/fixture.yaml"

if [ ! -f "$fixture_yaml" ]; then
  echo "eval-prompt-no-hints prepare: no fixture.yaml beside $path — every eval prompt.md belongs to a fixture that declares its seed/overlay" >&2
  exit 1
fi

overlay=$(grep -E '^overlay:' "$fixture_yaml" | sed -E 's/^overlay:[[:space:]]*//')
seed=$(grep -E '^seed:' "$fixture_yaml" | sed -E 's/^seed:[[:space:]]*//')
base="${overlay:-$seed}"

# One entry per rule/skill the fixture ships, each {name, kind, body} — the
# body is what lets the judge recognize a paraphrase of what the rule does,
# not just a paraphrase of its directory name.
entries="[]"
if [ -n "$base" ] && [ -d "$fixture_dir/$base" ]; then
  for f in "$fixture_dir/$base"/.sloprail/*/*/*.yaml; do
    [ -f "$f" ] || continue
    name=$(basename "$(dirname "$f")")
    kind=$(basename "$(dirname "$(dirname "$f")")")
    body=$(cat "$f")
    entry=$(jq -n --arg name "$name" --arg kind "$kind" --arg body "$body" \
      '{name: $name, kind: $kind, body: $body}')
    entries=$(printf '%s' "$entries" | jq --argjson e "$entry" '. + [$e]')
  done
  for f in "$fixture_dir/$base"/.claude/skills/*/SKILL.md; do
    [ -f "$f" ] || continue
    name=$(basename "$(dirname "$f")")
    body=$(cat "$f")
    entry=$(jq -n --arg name "$name" --arg body "$body" \
      '{name: $name, kind: "skill", body: $body}')
    entries=$(printf '%s' "$entries" | jq --argjson e "$entry" '. + [$e]')
  done
fi

jq -n --argjson entries "$entries" '{additionalContext: {fixture_rules: $entries}}'
