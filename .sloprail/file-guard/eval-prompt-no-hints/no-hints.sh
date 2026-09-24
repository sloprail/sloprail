#!/usr/bin/env bash
# Refuses a prompt.md that names the gate/skill/context its own fixture
# exists to prove, or the .sloprail/ machinery generally. The agent-under-test
# is supposed to discover the requirement by running into it, not be told.
set -uo pipefail

payload="$(cat)"

result_known=$(printf '%s' "$payload" | jq -r '.event.resultKnown')
if [ "$result_known" != "true" ]; then
  echo '{"reason":"the engine could not derive what this write would leave behind, so this cannot be checked before it lands — rerun after the write to get the after-check instead"}'
  exit 1
fi

path=$(printf '%s' "$payload" | jq -r '.event.path')
content=$(printf '%s' "$payload" | jq -r '.event.newContent')

if [ -z "${SR_WORKSPACE:-}" ]; then
  echo '{"reason":"SR_WORKSPACE is not set — cannot locate the fixture this prompt belongs to"}'
  exit 1
fi

fixture_dir="$SR_WORKSPACE/$(dirname "$path")"
fixture_yaml="$fixture_dir/fixture.yaml"

if [ ! -f "$fixture_yaml" ]; then
  echo '{"reason":"no fixture.yaml beside '"$path"' — every eval prompt.md belongs to a fixture that declares its seed/overlay"}'
  exit 1
fi

# The names a real agent must NOT be handed: every gate/context/file-guard rule
# name and every skill name that fixture.yaml's overlay (or seed, for a fixture
# with no separate overlay) actually ships. Directory names ARE the rule/skill
# names per the authoring-guardrails skill ("the folder name IS the rule's
# name"), so this is a plain find, not a YAML parse.
overlay=$(grep -E '^overlay:' "$fixture_yaml" | sed -E 's/^overlay:[[:space:]]*//')
seed=$(grep -E '^seed:' "$fixture_yaml" | sed -E 's/^seed:[[:space:]]*//')
base="${overlay:-$seed}"

names=""
if [ -n "$base" ] && [ -d "$fixture_dir/$base" ]; then
  # Two different depths, not one shared find: a gate/context/file-guard name
  # is .sloprail/<nature>/<name>/ (2 levels below .sloprail), but a skill name
  # is .claude/skills/<name>/ (1 level below .claude/skills) — sharing one
  # -mindepth/-maxdepth silently missed every skill name, which is how a
  # prompt naming the required skill outright passed this check uncaught.
  rule_names=$(find "$fixture_dir/$base/.sloprail" \
    -mindepth 2 -maxdepth 2 -type d 2>/dev/null | xargs -n1 basename 2>/dev/null)
  skill_names=$(find "$fixture_dir/$base/.claude/skills" \
    -mindepth 1 -maxdepth 1 -type d 2>/dev/null | xargs -n1 basename 2>/dev/null)
  names=$(printf '%s\n%s\n' "$rule_names" "$skill_names" | sort -u)
fi

hits=""
if [ -n "$names" ]; then
  while IFS= read -r name; do
    [ -z "$name" ] && continue
    if printf '%s' "$content" | grep -qiF "$name"; then
      hits="$hits $name"
    fi
  done <<< "$names"
fi

# Generic nature words, independent of any specific fixture's own rule names —
# a prompt that talks ABOUT sloprail's machinery at all has told the agent
# there is a guardrail to find, which is its own kind of hint.
for word in ".sloprail" "guardrail" "gate" "skill required" "sr-session"; do
  if printf '%s' "$content" | grep -qiF "$word"; then
    hits="$hits [$word]"
  fi
done

if [ -n "$hits" ]; then
  echo '{"reason":"this prompt mentions:'"$hits"' — an eval prompt must read like an ordinary task request, with no hint that a guardrail, a skill, or sloprail itself is involved; rephrase around the real-world outcome instead"}'
  exit 1
fi

exit 0
