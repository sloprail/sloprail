#!/usr/bin/env bash
set -euo pipefail
# invariant-upheld on the CI path (sr-checks run over a committed range), each refusal beside its
# nearest permitted neighbour. The base holds demo/a, demo/b and demo/c, each with marked code.
# 1. the judge decides: marked code given a bypass branch is refused with the judge's reasoning;
#    the same change without it passes.
# 2. one subject per invariant: a file marked for demo/a and demo/b, changed, judges each alone;
#    demo/c, untouched, is not judged.
# 3. an invariant reworded alone is judged against its marked code at head.
# 4. a test changing alone judges nothing here (that is invariant-rigor's).
git init -q .
mkdir -p .sloprail/file-guard
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
# Only this rule judges here: the case project starts with every project rule.
rm -rf .sloprail/file-guard/spec-quality .sloprail/file-guard/invariant-covered .sloprail/file-guard/invariant-rigor
cp -R "$SR_TEST_SLOPRAIL_DIR/_lib" .sloprail/
cp -R "$SR_TEST_SLOPRAIL_DIR/file-guard/invariant-upheld" .sloprail/file-guard/
rm -rf .sloprail/file-guard/invariant-upheld/tests
inv() { mkdir -p spec/demo/invariants; printf 'predicate: A {@fld:demo:Thing.on} thing %s holds.\nwhy: w\n' "$1" > "spec/demo/invariants/$1.yaml"; }
inv a; inv b; inv c
mkdir -p src
printf 'package src\n\n// sr:invariant demo/a\nfunc A() {}\n' > src/a.go
printf 'package src\n\n// sr:invariant demo/a\n// sr:invariant demo/b\nfunc AB() {}\n' > src/ab.go
printf 'package src\n\n// sr:invariant demo/c\nfunc C() {}\n' > src/c.go
printf 'package src\n\n// sr:proves demo/a\nfunc TestA(t *testing.T) {}\n' > src/a_test.go
git add -A && git -c user.name=t -c user.email=t@t commit -q -m base
BASE=$(git rev-parse HEAD)
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")
export JUDGE_LOG="$(mktemp)"
export SR_CHECKS_JUDGE_MOCKS=$(jq -nc --arg p "$SR_TEST_CASE_DIR/judge-mock.sh" '{"file-guard/invariant-upheld/code-upholds-invariant": $p}')

commit() { git add -A && git -c user.name=t -c user.email=t@t commit -q -m "$1"; }
run() { : > "$SR_EVENTS_FILE"; : > "$JUDGE_LOG"; sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1; }
dump() { jq -c . "$SR_EVENTS_FILE" >&2; cat "$JUDGE_LOG" >&2; echo "$1" >&2; exit 1; }
passed() { jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="invariant-upheld" and .outcome=="passed") and (any(.[]; .kind=="FileGuardChecked" and .rule=="invariant-upheld" and .outcome=="refused") | not)' "$SR_EVENTS_FILE" >/dev/null; }

# 1. the judge decides
git checkout -q -b bypass "$BASE"
printf 'package src\n\n// sr:invariant demo/a\nfunc A() { if bypass { return } }\n' > src/a.go
commit bypass
if run; then dump "1: marked code with a bypass passed"; fi
jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="invariant-upheld" and .outcome=="refused" and (.reason|contains("MOCK: the bypass branch breaks the invariant")))' < "$SR_EVENTS_FILE" >/dev/null || dump "1: no refusal carrying the judge's reasoning"
printf 'package src\n\n// sr:invariant demo/a\nfunc A() { if on { return } }\n' > src/a.go
commit no-bypass
run || dump "1: the change without a bypass was refused"
passed || dump "1: no passed verdict without the bypass"

# 2. one subject per invariant, untouched ones left alone
git checkout -q -b shared "$BASE"
printf 'package src\n\n// sr:invariant demo/a\n// sr:invariant demo/b\nfunc AB() { _ = 1 }\n' > src/ab.go
commit shared
run || dump "2: a sound change to shared code was refused"
passed || dump "2: no passed verdict for the shared change"
[ "$(sort "$JUDGE_LOG" | tr '\n' '|')" = "demo/a|demo/b|" ] || dump "2: expected one judge call each for demo/a and demo/b, the judge saw: $(tr '\n' '|' < "$JUDGE_LOG")"

# 3. the invariant reworded alone
git checkout -q -b reword "$BASE"
printf 'predicate: A {@fld:demo:Thing.on} thing c holds, reworded.\nwhy: w\n' > spec/demo/invariants/c.yaml
commit reword
run || dump "3: a rewording its code still upholds was refused"
[ "$(cat "$JUDGE_LOG")" = "demo/c" ] || dump "3: expected one judge call for demo/c, the judge saw: $(tr '\n' '|' < "$JUDGE_LOG")"

# 4. a test alone is not this rule's
git checkout -q -b test-only "$BASE"
printf 'package src\n\n// sr:proves demo/a\nfunc TestA(t *testing.T) { _ = 1 }\n' > src/a_test.go
commit test-only
run || dump "4: a test-only change was refused"
[ ! -s "$JUDGE_LOG" ] || dump "4: the judge ran on a test-only change: $(cat "$JUDGE_LOG")"
