#!/usr/bin/env bash
# Assembles what the judge needs to reason about paraphrased hints —
# no-hints.sh's own literal grep only catches a fixture's exact rule/skill
# names, and cannot tell "load the credential-tracking skill first" is just
# as much a hint as "load update-credential-inventory first". The judge
# needs the actual gate/skill bodies to recognize a paraphrase, not just
# their directory names.
# SR_TREE is the read-only snapshot of the committed head: the fixture is read as
# committed, not as half-edited in the working tree.
set -uo pipefail

payload="$(cat)"

if [ -z "${SR_TREE:-}" ]; then
  echo "eval-prompt-no-hints prepare: SR_TREE is not set — cannot locate the fixtures these prompts belong to" >&2
  exit 1
fi

paths="$(printf '%s' "$payload" | jq -r '.changeset.files[].path')" || {
  echo "eval-prompt-no-hints prepare: the changeset could not be read, so no prompt was checked. REFUSING." >&2
  exit 1
}
if [ -z "$paths" ]; then
  echo "eval-prompt-no-hints prepare: the changeset holds no prompt.md, so there is nothing to judge" >&2
  exit 1
fi

# One entry per rule/skill each prompt's fixture ships, each {prompt, name, kind,
# body} — the body is what lets the judge recognize a paraphrase of what the rule
# does, not just a paraphrase of its directory name. `prompt` ties the entry to
# the prompt it is checked against; the changeset may hold several fixtures'.
entries="[]"
while IFS= read -r path; do
  fixture_dir="$SR_TREE/$(dirname "$path")"
  fixture_yaml="$fixture_dir/fixture.yaml"
  if [ ! -f "$fixture_yaml" ]; then
    echo "eval-prompt-no-hints prepare: no fixture.yaml beside $path — every eval prompt.md belongs to a fixture that declares its seed/overlay" >&2
    exit 1
  fi

  overlay=$(grep -E '^overlay:' "$fixture_yaml" | sed -E 's/^overlay:[[:space:]]*//')
  seed=$(grep -E '^seed:' "$fixture_yaml" | sed -E 's/^seed:[[:space:]]*//')
  base="${overlay:-$seed}"

  if [ -n "$base" ] && [ -d "$fixture_dir/$base" ]; then
    for f in "$fixture_dir/$base"/.sloprail/*/*/*.yaml; do
      [ -f "$f" ] || continue
      name=$(basename "$(dirname "$f")")
      kind=$(basename "$(dirname "$(dirname "$f")")")
      body=$(cat "$f") || { echo "eval-prompt-no-hints prepare: could not read $f" >&2; exit 1; }
      entry=$(jq -n --arg prompt "$path" --arg name "$name" --arg kind "$kind" --arg body "$body" \
        '{prompt: $prompt, name: $name, kind: $kind, body: $body}') || exit 1
      entries=$(printf '%s' "$entries" | jq --argjson e "$entry" '. + [$e]') || exit 1
    done
    for f in "$fixture_dir/$base"/.claude/skills/*/SKILL.md; do
      [ -f "$f" ] || continue
      name=$(basename "$(dirname "$f")")
      body=$(cat "$f") || { echo "eval-prompt-no-hints prepare: could not read $f" >&2; exit 1; }
      entry=$(jq -n --arg prompt "$path" --arg name "$name" --arg body "$body" \
        '{prompt: $prompt, name: $name, kind: "skill", body: $body}') || exit 1
      entries=$(printf '%s' "$entries" | jq --argjson e "$entry" '. + [$e]') || exit 1
    done
  fi
done <<<"$paths"

jq -n --argjson entries "$entries" '{additionalContext: {fixture_rules: $entries}}'
