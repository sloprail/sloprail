#!/usr/bin/env bash
set -euo pipefail
# spec-quality on the CI path: an invariant that breaks spec.cue (no typed mention, an unknown key)
# or cites an entity that does not exist is refused before any judge; 27 well-formed invariants
# citing one entity are split into two buckets (25 + 3 with the entity), each judged once, and pass.
git init -q .
mkdir -p .sloprail/file-guard .claude/skills
cp -R "$SR_TEST_SLOPRAIL_DIR/file-guard/spec-quality" .sloprail/file-guard/
rm -rf .sloprail/file-guard/spec-quality/tests
cp -R "$SR_TEST_SLOPRAIL_DIR/../.claude/skills/document-invariant" "$SR_TEST_SLOPRAIL_DIR/../.claude/skills/document-entity" .claude/skills/
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m rules
BASE=$(git rev-parse HEAD)
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)
export JUDGE_LOG="$PWD/judge.log"
export SR_CHECKS_JUDGE_MOCKS=$(jq -nc --arg p "$SR_TEST_CASE_DIR/judge-pass.sh" '{"file-guard/spec-quality/judge": $p}')
refused() { jq -es --arg w "$1" 'any(.[]; .kind=="FileGuardChecked" and .rule=="spec-quality" and .outcome=="refused" and (.reason|contains($w)))' "$SR_EVENTS_FILE" >/dev/null; }

# 1a. malformed shape: no typed mention, an unknown key
: > "$SR_EVENTS_FILE"; : > "$JUDGE_LOG"
git checkout -q -b shape "$BASE"
mkdir -p spec/demo/invariants
printf 'predicate: no anchor here\nwhy: w\nextra: 1\n' > spec/demo/invariants/bad-shape.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m bad-shape
if sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1; then echo "a malformed invariant passed" >&2; exit 1; fi
refused "bad-shape.yaml" || { jq -c . "$SR_EVENTS_FILE" >&2; echo "no refusal naming bad-shape.yaml" >&2; exit 1; }

# 1b. a mention of an entity that does not exist
: > "$SR_EVENTS_FILE"
git checkout -q -b mention "$BASE"
mkdir -p spec/demo/invariants
printf 'predicate: A {@ent:demo:Missing} holds.\nwhy: w\n' > spec/demo/invariants/bad-mention.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m bad-mention
if sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1; then echo "an unresolved mention passed" >&2; exit 1; fi
refused "spec/demo/entities/Missing.yaml" || { jq -c . "$SR_EVENTS_FILE" >&2; echo "no refusal naming the missing entity" >&2; exit 1; }
[ ! -s "$JUDGE_LOG" ] || { echo "the judge ran on a bucket the deterministic checks refused" >&2; exit 1; }

# 2. well-formed: one entity, 27 invariants citing it -> two buckets, both judged, both pass
: > "$SR_EVENTS_FILE"; : > "$JUDGE_LOG"
git checkout -q -b good "$BASE"
mkdir -p spec/demo/entities spec/demo/invariants
printf 'doc: A demo thing.\nfields:\n  - name: on\n    type: bool\n' > spec/demo/entities/Thing.yaml
for i in $(seq -w 1 27); do printf 'predicate: A {@fld:demo:Thing.on} thing %s holds.\nwhy: w\n' "$i" > spec/demo/invariants/i$i.yaml; done
git add -A && git -c user.name=t -c user.email=t@t commit -q -m good
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 || { jq -c . "$SR_EVENTS_FILE" >&2; echo "well-formed spec was refused" >&2; exit 1; }
[ "$(sort -n "$JUDGE_LOG" | tr '\n' ' ')" = "3 25 " ] || { echo "expected buckets of 3 and 25, judge saw: $(cat "$JUDGE_LOG")" >&2; exit 1; }
