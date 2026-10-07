#!/usr/bin/env bash
set -euo pipefail
# invariant-covered on the CI path (sr-checks run over a committed range), each refusal beside its
# nearest permitted neighbour. The base holds demo/a (implemented and proven) and demo/old (neither:
# it stood before the rule and is taken as it is).
# 1. a new invariant with no markers is refused naming both missing halves; marked, it passes.
# 2. deleting the only test proving demo/a is refused; deleting an unmarked test passes.
# 3. a marker naming no invariant is refused naming the missing spec file.
# 4. sr:proves on code (not a test) is refused.
# 5. touching demo/a alone passes although demo/old, untouched, has no markers.
git init -q .
mkdir -p .sloprail/file-guard
cp -R "$SR_TEST_SLOPRAIL_DIR/_lib" .sloprail/
cp -R "$SR_TEST_SLOPRAIL_DIR/file-guard/invariant-covered" .sloprail/file-guard/
rm -rf .sloprail/file-guard/invariant-covered/tests
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
# Only this rule judges here: the case project starts with every project rule.
rm -rf .sloprail/file-guard/spec-quality .sloprail/file-guard/invariant-rigor .sloprail/file-guard/invariant-upheld
inv() { mkdir -p "spec/demo/invariants"; printf 'predicate: A {@fld:demo:Thing.on} thing %s holds.\nwhy: w\n' "$1" > "spec/demo/invariants/$1.yaml"; }
inv a; inv old
mkdir -p src
printf 'package src\n\n// sr:invariant demo/a\nfunc A() {}\n' > src/a.go
printf 'package src\n\n// sr:proves demo/a\nfunc TestA(t *testing.T) {}\n' > src/a_test.go
printf 'package src\n\nfunc TestOther(t *testing.T) {}\n' > src/other_test.go
git add -A && git -c user.name=t -c user.email=t@t commit -q -m base
BASE=$(git rev-parse HEAD)
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
export CLAUDE_CONFIG_DIR=$(echo "$SETUP" | jq -er .config_dir)
export CLAUDE_CODE_PLUGIN_CACHE_DIR=$(echo "$SETUP" | jq -er .plugin_cache)

commit() { git add -A && git -c user.name=t -c user.email=t@t commit -q -m "$1"; }
run() { : > "$SR_EVENTS_FILE"; sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1; }
dump() { jq -c . "$SR_EVENTS_FILE" >&2; echo "$1" >&2; exit 1; }
# refused WORD...: one invariant-covered refusal whose reason carries every WORD
refused() { jq -es --args 'any(.[]; .kind=="FileGuardChecked" and .rule=="invariant-covered" and .outcome=="refused" and (.reason as $r | $ARGS.positional | all(. as $w | $r | contains($w))))' "$@" < "$SR_EVENTS_FILE" >/dev/null; }
passed() { jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="invariant-covered" and .outcome=="passed") and (any(.[]; .kind=="FileGuardChecked" and .rule=="invariant-covered" and .outcome=="refused") | not)' "$SR_EVENTS_FILE" >/dev/null; }

# 1. a new invariant, unmarked, then marked
git checkout -q -b new "$BASE"
inv b
commit add-b
if run; then dump "1: an invariant with no markers passed"; fi
refused "invariant 'demo/b' has no implementation" "invariant 'demo/b' has no test" || dump "1: no refusal naming both missing halves of demo/b"
jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="invariant-covered" and .outcome=="refused" and (.reason|contains("Invariants not implemented or not proven")))' < "$SR_EVENTS_FILE" >/dev/null || dump "1: the refusal is not the coverage check's"
printf 'package src\n\n// sr:invariant demo/b\nfunc B() {}\n' > src/b.go
printf 'package src\n\n// sr:proves demo/b\nfunc TestB(t *testing.T) {}\n' > src/b_test.go
commit mark-b
run || dump "1: demo/b, implemented and proven, was refused"
passed || dump "1: no passed verdict once demo/b is marked"

# 2. losing the only proving test
git checkout -q -b lose "$BASE"
git rm -q src/a_test.go
commit drop-proof
if run; then dump "2: deleting the only test of demo/a passed"; fi
refused "invariant 'demo/a' has no test" || dump "2: no refusal naming demo/a's missing test"
git checkout -q -b lose-other "$BASE"
git rm -q src/other_test.go
printf 'package src\n\n// sr:invariant demo/a\nfunc A() { _ = 1 }\n' > src/a.go
commit drop-unmarked
run || dump "2: deleting an unmarked test was refused"
passed || dump "2: no passed verdict for the unmarked delete"

# 3. a marker naming no invariant
git checkout -q -b unknown "$BASE"
printf 'package src\n\n// sr:invariant demo/zz\nfunc Z() {}\n' > src/z.go
commit unknown
if run; then dump "3: a marker naming no invariant passed"; fi
refused "sr:invariant 'demo/zz' names no spec/demo/invariants/zz.yaml" || dump "3: no refusal naming the missing spec file"

# 4. sr:proves on code
git checkout -q -b misplaced "$BASE"
printf 'package src\n\n// sr:invariant demo/a\n// sr:proves demo/a\nfunc A() {}\n' > src/a.go
commit misplaced
if run; then dump "4: sr:proves on code passed"; fi
refused "src/a.go: sr:proves belongs on a test" || dump "4: no refusal naming the misplaced sr:proves"

# 5. an untouched, uncovered invariant is taken as it is
git checkout -q -b untouched "$BASE"
printf 'package src\n\n// sr:invariant demo/a\nfunc A() { _ = 2 }\n' > src/a.go
commit touch-a
run || dump "5: touching demo/a was refused although demo/old was not touched"
passed || dump "5: no passed verdict when only demo/a is touched"
