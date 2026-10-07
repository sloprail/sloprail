#!/usr/bin/env bash
set -euo pipefail
# invariant-rigor on the CI path (sr-checks run over a committed range), each refusal beside its
# nearest permitted neighbour. The base holds demo/a and demo/b, each with code and a test.
# 1. the judge decides: a new test for demo/a that asserts nothing is refused with the judge's
#    reasoning; the same test asserting passes.
# 2. one subject per invariant: changing demo/a's test and demo/b's spec judges two subjects,
#    each alone, and an untouched invariant is not judged.
# 3. code marked sr:invariant changing alone judges nothing here (that is invariant-upheld's).
git init -q .
mkdir -p .sloprail/file-guard
cp -R "$SR_TEST_SLOPRAIL_DIR/_lib" .sloprail/
cp -R "$SR_TEST_SLOPRAIL_DIR/file-guard/invariant-rigor" .sloprail/file-guard/
rm -rf .sloprail/file-guard/invariant-rigor/tests
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
# Only this rule judges here: the case project starts with every project rule.
rm -rf .sloprail/file-guard/spec-quality .sloprail/file-guard/invariant-covered .sloprail/file-guard/invariant-upheld
inv() { mkdir -p spec/demo/invariants; printf 'predicate: A {@fld:demo:Thing.on} thing %s holds.\nwhy: w\n' "$1" > "spec/demo/invariants/$1.yaml"; }
inv a; inv b; inv c
mkdir -p src
for n in a b c; do
  printf 'package src\n\n// sr:invariant demo/%s\nfunc F%s() {}\n' "$n" "$n" > "src/$n.go"
  printf 'package src\n\n// sr:proves demo/%s\nfunc Test%s(t *testing.T) { if F%s; false { t.Fatal() } }\n' "$n" "$n" "$n" > "src/${n}_test.go"
done
git add -A && git -c user.name=t -c user.email=t@t commit -q -m base
BASE=$(git rev-parse HEAD)
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")
export JUDGE_LOG="$(mktemp)"
export SR_CHECKS_JUDGE_MOCKS=$(jq -nc --arg p "$SR_TEST_CASE_DIR/judge-mock.sh" '{"file-guard/invariant-rigor/tests-prove-statement": $p}')

commit() { git add -A && git -c user.name=t -c user.email=t@t commit -q -m "$1"; }
run() { : > "$SR_EVENTS_FILE"; : > "$JUDGE_LOG"; sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1; }
dump() { jq -c . "$SR_EVENTS_FILE" >&2; cat "$JUDGE_LOG" >&2; echo "$1" >&2; exit 1; }
passed() { jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="invariant-rigor" and .outcome=="passed") and (any(.[]; .kind=="FileGuardChecked" and .rule=="invariant-rigor" and .outcome=="refused") | not)' "$SR_EVENTS_FILE" >/dev/null; }

# 1. the judge decides
git checkout -q -b empty "$BASE"
printf 'package src\n\n// sr:proves demo/a\nfunc TestA2(t *testing.T) {}\n' > src/a2_test.go
commit empty-test
if run; then dump "1: a test asserting nothing passed"; fi
jq -es 'any(.[]; .kind=="FileGuardChecked" and .rule=="invariant-rigor" and .outcome=="refused" and (.reason|contains("MOCK: src/a2_test.go asserts nothing")))' < "$SR_EVENTS_FILE" >/dev/null || dump "1: no refusal carrying the judge's reasoning"
printf 'package src\n\n// sr:proves demo/a\nfunc TestA2(t *testing.T) { if Fa; false { t.Fatal() } }\n' > src/a2_test.go
commit asserting-test
run || dump "1: the asserting test was refused"
passed || dump "1: no passed verdict for the asserting test"

# 2. one subject per invariant, untouched ones left alone
git checkout -q -b two "$BASE"
printf 'package src\n\n// sr:proves demo/a\nfunc TestA(t *testing.T) { if Fa; true { t.Log() } }\n' > src/a_test.go
printf 'predicate: A {@fld:demo:Thing.on} thing b still holds.\nwhy: w\n' > spec/demo/invariants/b.yaml
commit two
run || dump "2: two well-proven invariants were refused"
passed || dump "2: no passed verdict for the two subjects"
[ "$(sort "$JUDGE_LOG" | tr '\n' '|')" = "demo/a |demo/b |" ] || dump "2: expected one judge call each for demo/a and demo/b, the judge saw: $(tr '\n' '|' < "$JUDGE_LOG")"

# 3. code alone is not this rule's
git checkout -q -b code "$BASE"
printf 'package src\n\n// sr:invariant demo/c\nfunc Fc() { _ = 1 }\n' > src/c.go
commit code-only
run || dump "3: a code-only change was refused"
[ ! -s "$JUDGE_LOG" ] || dump "3: the judge ran on a code-only change: $(cat "$JUDGE_LOG")"
