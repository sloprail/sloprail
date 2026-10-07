#!/usr/bin/env bash
set -euo pipefail
# rule-tests-pass, the cache. A verdict is cached on the rule, the changed files and the subject's fingerprint,
# so the fingerprint must move when anything the cases can depend on moves, even a file that is in no range. The
# rule's own subjects.sh is run on the same change over trees that differ in one file at a time:
#   1. the same tree twice gives the same fingerprint (so a verdict is reused);
#   2. a shared file (.sloprail/lib/flag.txt), which the change does not touch, changes it for every rule of the
#      root;
#   3. a file of another rule that the subject's rule uses (its own files contain `/beta`) changes it;
#   4. a file of a rule that the subject's rule does not use leaves it alone;
#   5. judged for real by sr-checks, an edit of one rule passes, and a shared-file edit refuses the rule whose
#      case fails, naming it alone.
git init -q .
# one agent run with no turn sets up the plugin (config dir and plugin cache) so the rule's scripts are on disk
SETUP=$(sr-test agent "$SR_TEST_CASE_DIR/agent.sh" --prompt "set up")
while IFS= read -r kv; do export "$kv"; done < <(echo "$SETUP" | jq -er ".env[]")
# the sandbox links the plugin under test beside its config dir
SUBJECTS="$(dirname "$CLAUDE_CONFIG_DIR")/marketplace/plugins/sloprail/.sloprail/file-guard/rule-tests-pass/subjects.sh"
if [ ! -f "$SUBJECTS" ]; then
  echo "no rule-tests-pass/subjects.sh at $SUBJECTS" >&2
  exit 1
fi

mkdir -p .sloprail/lib .sloprail/gate/alpha/tests/c .sloprail/gate/beta/tests/c .sloprail/gate/gamma/tests/c
printf 'ok\n' > .sloprail/lib/flag.txt
printf '#!/usr/bin/env bash\n# uses ../beta/x.sh\nexit 0\n' > .sloprail/gate/alpha/refuse.sh
printf '#!/usr/bin/env bash\nexit 0\n' > .sloprail/gate/beta/refuse.sh
printf 'x\n' > .sloprail/gate/beta/x.sh
printf '#!/usr/bin/env bash\nexit 0\n' > .sloprail/gate/gamma/refuse.sh
for r in alpha beta gamma; do
  printf 'on:\n  - event: PreFileWrite\n    match: '"'"'event.path startsWith "notes/"'"'"'\nchecks:\n  - script: ./refuse.sh\n' > .sloprail/gate/$r/gate.yaml
  printf '#!/usr/bin/env bash\nexit 0\n' > .sloprail/gate/$r/tests/c/test.sh
  chmod +x .sloprail/gate/$r/refuse.sh .sloprail/gate/$r/tests/c/test.sh
done

# the change under judgement: alpha's script edited (alpha is the subject whose fingerprint is read)
PAYLOAD='{"changeset":{"files":[{"path":".sloprail/gate/alpha/refuse.sh","status":"M","oldContent":"a","newContent":"b"}]}}'
fingerprint() {
  printf '%s' "$PAYLOAD" | SR_TREE="$PWD" bash "$SUBJECTS" | jq -er '.[] | select(.id == ".sloprail/gate/alpha") | .fingerprint'
}

FP0=$(fingerprint)
[ "$(fingerprint)" = "$FP0" ]

printf 'bad\n' > .sloprail/lib/flag.txt
FP1=$(fingerprint)
[ "$FP1" != "$FP0" ]
printf 'ok\n' > .sloprail/lib/flag.txt
[ "$(fingerprint)" = "$FP0" ]

printf 'y\n' > .sloprail/gate/beta/x.sh
FP2=$(fingerprint)
[ "$FP2" != "$FP0" ]
printf 'x\n' > .sloprail/gate/beta/x.sh

printf '#!/usr/bin/env bash\n# edited\nexit 0\n' > .sloprail/gate/gamma/refuse.sh
[ "$(fingerprint)" = "$FP0" ]

# 5. the same project judged for real: an edit of alpha passes while every case passes; an edit that also touches a
# shared file fans out to every rule of the root, and is refused for the one rule whose case fails
printf 'x\n' > .sloprail/gate/beta/x.sh
printf '#!/usr/bin/env bash\nexit 0\n' > .sloprail/gate/gamma/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "a project with three rules"
BASE=$(git rev-parse HEAD)
printf '# edited\n' >> .sloprail/gate/alpha/refuse.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "edit alpha"
: > "$SR_EVENTS_FILE"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==1 and .[0].outcome=="passed"' "$SR_EVENTS_FILE" >/dev/null
printf '#!/usr/bin/env bash\necho "broken in the base" >&2\nexit 1\n' > .sloprail/gate/gamma/tests/c/test.sh
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "gamma broken"
BASE=$(git rev-parse HEAD)
printf 'changed\n' >> .sloprail/lib/flag.txt
git add -A && git -c user.name=t -c user.email=t@t commit -q -m "edit the shared lib"
sr-checks run --base "$BASE" --head HEAD >/dev/null 2>&1 && status=0 || status=$?
[ "$status" -ne 0 ]
jq -es '[.[]|select(.kind=="FileGuardChecked" and .rule=="sloprail/rule-tests-pass")] | length==2 and .[1].outcome=="refused" and (.[1].reason|contains("gate/gamma:c: fail")) and (.[1].reason|contains("broken in the base")) and (.[1].reason|contains("gate/alpha:")|not)' "$SR_EVENTS_FILE" >/dev/null
