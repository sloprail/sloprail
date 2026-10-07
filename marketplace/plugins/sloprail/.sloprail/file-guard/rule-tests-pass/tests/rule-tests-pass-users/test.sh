#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, what else runs a rule's cases. Two gates: "alpha" has a broken case (committed so in the base,
# untouched since) and sources beta's lib in the SPLIT form (a directory variable, then a file under it, with no
# `beta/lib.sh` anywhere in one piece); "beta" has a passing case.
#   1. a range that edits only beta's lib.sh judges alpha too (alpha's files contain `/beta`): refused, naming
#      gate/alpha:broken, and not gate/beta (beta's own case passes);
#   2. a range that edits only .sloprail/config.yaml makes every rule of the root a subject: refused the same
#      way;
#   3. alpha's case fixed: the config edit passes.
F="$SR_TEST_CASE_DIR/fixtures"
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so sr-checks loads sloprail's rules here
git -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")
: > "$SR_EVENTS_FILE"
mkdir -p .sloprail/file-guard
mkdir -p .sloprail/gate/alpha/tests/broken .sloprail/gate/beta/tests/good
for r in alpha beta; do
  cat "$F/demo-gate.yaml" > .sloprail/gate/$r/gate.yaml
  cat "$F/demo-refuse.txt" > .sloprail/gate/$r/refuse.sh
  chmod +x .sloprail/gate/$r/refuse.sh
done
# the split form: the directory in one statement, the file under it in another
{
  printf 'lib_dir="$(cd "$(dirname "$0")/../beta" && pwd)"\n'
  printf '. "$lib_dir/lib.sh"\n'
} >> .sloprail/gate/alpha/refuse.sh
printf '# a lib alpha sources\n' > .sloprail/gate/beta/lib.sh
printf 'disabled: []\n' > .sloprail/config.yaml
cat "$F/failing.txt" > .sloprail/gate/alpha/tests/broken/test.sh
cat "$F/passing.txt" > .sloprail/gate/beta/tests/good/test.sh
chmod +x .sloprail/gate/alpha/tests/broken/test.sh .sloprail/gate/beta/tests/good/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "alpha broken, beta fine, a lib and a config"
BASE=$(git rev-parse HEAD)

# 1. beta's lib, which alpha sources in the split form: alpha's broken case is judged
printf '# edited\n' >> .sloprail/gate/beta/lib.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "edit beta's lib"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==1 and .[0].outcome=="refused" and (.[0].reason|contains("gate/alpha:broken: fail")) and (.[0].reason|contains("gate/beta:")|not)' "$SR_EVENTS_FILE" >/dev/null

# 2. a new base, still with alpha broken: only config.yaml changes
BASE=$(git rev-parse HEAD)
printf 'disabled: []\n# edited\n' > .sloprail/config.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "edit the config"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==2 and .[1].outcome=="refused" and (.[1].reason|contains("gate/alpha:broken: fail")) and (.[1].reason|contains("gate/beta:")|not)' "$SR_EVENTS_FILE" >/dev/null

# 3. the recovery: alpha's case is fixed, the config edit passes
cat "$F/passing.txt" > .sloprail/gate/alpha/tests/broken/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "fix alpha's case"
BASE=$(git rev-parse HEAD)
printf 'disabled: []\n# edited again\n' > .sloprail/config.yaml
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "edit the config again"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==3 and .[2].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
