#!/usr/bin/env bash
set -euo pipefail

git init -q -b main .
mkdir -p .sloprail tests/e2e/a tests/e2e/b tests/e2e/big tests/e2e/many tests/e2e/ten
printf 'disabled:\n  - sloprail/file-guard/rule-tests-pass\n' > .sloprail/config.yaml
# a: 2 tests; b: 1 test; ten: 10 tests; big: 12 tests named TestBigNN; many: 11 tests named TestManyNN
gen() { f=$1; shift; { echo 'package x'; for n in "$@"; do printf 'func %s(t *testing.T) {}\n' "$n"; done; } > "$f"; }
gen tests/e2e/a/a_test.go TestA1 TestA2
gen tests/e2e/b/b_test.go TestB1
gen tests/e2e/big/big_test.go $(printf 'TestBig%02d ' $(seq 1 12))
gen tests/e2e/many/many_test.go $(printf 'TestMany%02d ' $(seq 1 11))
gen tests/e2e/ten/ten_test.go $(printf 'TestTen%02d ' $(seq 1 10))
git add -A && git -c user.name=t -c user.email=t@t commit -q -m init
RESULT=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "run the e2e tests")
# every assertion is one jq -e over this rule's own GateChecked events, selected by tool_use_id
refused() { echo "$RESULT" | jq -e --arg id "$1" --arg t "$2" '[.events[]|select(.kind=="GateChecked" and .rule=="no-local-e2e-suite" and .tool_use_id==$id)] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains($t))' >/dev/null || { echo "FAIL refused $1: $(echo "$RESULT" | jq -c --arg id "$1" '[.events[]|select(.kind=="GateChecked" and .rule=="no-local-e2e-suite" and .tool_use_id==$id)]')" >&2; return 1; }; }
permitted() { echo "$RESULT" | jq -e --arg id "$1" '[.events[]|select(.kind=="GateChecked" and .rule=="no-local-e2e-suite" and .tool_use_id==$id)] | length==1 and .[0].outcome=="permitted"' >/dev/null || { echo "FAIL permitted $1: $(echo "$RESULT" | jq -c --arg id "$1" '[.events[]|select(.kind=="GateChecked" and .rule=="no-local-e2e-suite" and .tool_use_id==$id)]')" >&2; return 1; }; }
# too many tests: a `...` over everything (15), no -run over a 12-test package, a -run matching 11
refused c1 "would run 36 e2e tests"
refused c2 "would run 12 e2e tests"
refused c3 "would run 11 e2e tests"
refused c4 "would run 12 e2e tests"
# make targets, and the wrapped forms
refused c5 "Don't run the e2e suite locally"
refused c6 "Don't run the e2e suite locally"
refused c7 "would run 12 e2e tests"
refused c8 "would run 36 e2e tests"
# a count nobody can know: a variable in -run, a missing path, -list, a bad regex
refused u1 "Rewrite it into a resolvable form"
refused u2 "tests/e2e/nope does not exist"
refused u3 "Rewrite it into a resolvable form"
refused u4 "is not a valid pattern"
# permitted: 3 tests across 2 packages, one named test, unit packages, wrapped -test.run
for id in p1 p2 p3 p4 p5; do permitted $id; done
# the boundary: exactly 10 tests is permitted, a make with another target is not the e2e make
permitted p6
permitted p7
# recovery: the refused command, then its rewrite into a literal / narrowed form, permitted
refused r1 "Rewrite it into a resolvable form"
permitted r1ok
refused r2 "would run 12 e2e tests"
permitted r2ok
# an absolute cd to the workspace, and a single named test: permitted
permitted a1
# a lost word in a flag value beside literal unit packages is permitted; one that could be a package is not
permitted g1
permitted g2
refused g3 "Rewrite it into a resolvable form"
refused g4 "Rewrite it into a resolvable form"
