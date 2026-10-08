#!/usr/bin/env bash
set -euo pipefail
# spec-quality on the CI path (sr-checks run over a committed range), each refusal beside its
# nearest permitted neighbour:
# 1. an invariant breaking spec.cue (no typed mention, an unknown key) is refused by the shape
#    check, naming spec.cue and the field; the same invariant made well-formed passes.
# 2. a mention of an entity that does not exist is refused naming the missing entity file;
#    with the entity committed, the same invariant passes.
# 3. the judge decides: an invariant naming a harness event is refused with the judge's
#    reasoning; the same invariant reworded neutrally passes.
# 4. bucketing: 27 invariants citing one entity are judged as two buckets (25 and 3 files).
git init -q .
mkdir -p .sloprail/file-guard .claude/skills
cp -R "$SR_TEST_SLOPRAIL_DIR/file-guard/spec-quality" .sloprail/file-guard/
rm -rf .sloprail/file-guard/spec-quality/tests
cp -R "$SR_TEST_SLOPRAIL_DIR/../.claude/skills/document-invariant" "$SR_TEST_SLOPRAIL_DIR/../.claude/skills/document-entity" .claude/skills/
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
mkdir -p spec/demo/entities
printf 'doc: A demo thing.\nfields:\n  - name: on\n    type: bool\n' > spec/demo/entities/Thing.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m rules
BASE=$(git rev-parse HEAD)
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")
export JUDGE_LOG="$(mktemp)"
export SR_CHECKS_JUDGE_MOCKS=$(jq -nc --arg p "$SR_TEST_CASE_DIR/judge-mock.sh" '{"file-guard/spec-quality/judge": $p}')

commit() { git add -A && git -c user.name=t -c user.email=t@t commit -q -m "$1"; }
run() { : > "$SR_EVENTS_FILE"; : > "$JUDGE_LOG"; sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1; }
dump() { jq -c . "$SR_EVENTS_FILE" >&2; echo "$1" >&2; exit 1; }
# refused WORD...: one spec-quality refusal whose reason carries every WORD
refused() { jq -es --args 'any(.[]; .kind=="FileGuardChecked" and .rule=="spec-quality" and .outcome=="refused" and (.reason as $r | $ARGS.positional | all(. as $w | $r | contains($w))))' "$@" < "$SR_EVENTS_FILE" >/dev/null; }
passed() { jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="spec-quality" and .outcome=="passed") and (any(.[]; .kind=="FileGuardChecked" and .rule=="spec-quality" and .outcome=="refused") | not)' "$SR_EVENTS_FILE" >/dev/null; }

# 1. shape
git checkout -q -b shape "$BASE"
mkdir -p spec/demo/invariants
printf 'predicate: no anchor here\nwhy: w\nextra: 1\n' > spec/demo/invariants/a.yaml
commit bad-shape
if run; then dump "1: a malformed invariant passed"; fi
refused "do not match spec.cue" "spec/demo/invariants/a.yaml" "#Invariant.predicate" || dump "1: no shape refusal naming spec.cue and the predicate"
jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="spec-quality" and .outcome=="refused" and (.reason|contains("Spec files that do not match spec.cue")))' < "$SR_EVENTS_FILE" >/dev/null || dump "1: the refusal is not the shape check's"
[ ! -s "$JUDGE_LOG" ] || dump "1: the judge ran on a bucket the shape check refused"
printf 'predicate: A {@fld:demo:Thing.on} thing holds.\nwhy: w\n' > spec/demo/invariants/a.yaml
commit fixed-shape
run || dump "1: the well-formed neighbour was refused"
passed || dump "1: no passed verdict for the well-formed neighbour"

# 2. a mention of a missing entity
git checkout -q -b mention "$BASE"
mkdir -p spec/demo/invariants
printf 'predicate: A {@ent:demo:Missing} holds.\nwhy: w\n' > spec/demo/invariants/b.yaml
commit bad-mention
if run; then dump "2: an unresolved mention passed"; fi
refused "do not resolve" "spec/demo/entities/Missing.yaml" || dump "2: no refusal naming the missing entity file"
jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="spec-quality" and .outcome=="refused" and (.reason|contains("Spec mentions that do not resolve")))' < "$SR_EVENTS_FILE" >/dev/null || dump "2: the refusal is not the mention check's"
[ ! -s "$JUDGE_LOG" ] || dump "2: the judge ran on a bucket the mention check refused"
printf 'doc: A missing thing, now present.\nfields:\n  - name: on\n    type: bool\n' > spec/demo/entities/Missing.yaml
commit entity-added
run || dump "2: the invariant with its entity present was refused"
passed || dump "2: no passed verdict once the entity exists"

# 3. the judge decides
git checkout -q -b judge "$BASE"
mkdir -p spec/demo/invariants
printf 'predicate: A {@fld:demo:Thing.on} thing refuses PreToolUse.\nwhy: w\n' > spec/demo/invariants/c.yaml
commit harness-word
if run; then dump "3: an invariant naming a harness event passed"; fi
refused "MOCK: names the harness event PreToolUse" || dump "3: no refusal carrying the judge's reasoning"
jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="spec-quality" and .outcome=="refused" and (.reason|contains("MOCK: names the harness event PreToolUse")))' < "$SR_EVENTS_FILE" >/dev/null || dump "3: the refusal is not the judge's"
printf 'predicate: A {@fld:demo:Thing.on} thing refuses an action before it happens.\nwhy: w\n' > spec/demo/invariants/c.yaml
commit neutral
run || dump "3: the neutral rewording was refused"
passed || dump "3: no passed verdict for the neutral rewording"

# 4. bucketing
git checkout -q -b buckets "$BASE"
mkdir -p spec/demo/invariants
for i in $(seq -w 1 27); do printf 'predicate: A {@fld:demo:Thing.on} thing %s holds.\nwhy: w\n' "$i" > spec/demo/invariants/i$i.yaml; done
commit many
run || dump "4: 27 well-formed invariants were refused"
passed || dump "4: no passed verdict for the 27 invariants"
[ "$(sort -n "$JUDGE_LOG" | tr '\n' ' ')" = "2 25 " ] || dump "4: expected buckets of 25 and 2 judged files, the judge saw: $(tr '\n' ' ' < "$JUDGE_LOG")"
