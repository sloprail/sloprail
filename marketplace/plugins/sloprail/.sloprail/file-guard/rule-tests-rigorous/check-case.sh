#!/usr/bin/env bash
# rule-tests-rigorous, the deterministic floor. One subject = one case folder. It reads the committed
# test.sh (and the mock judges beside it) from SR_TREE and refuses the structural shapes that make a case
# pass whatever the rule does. What needs reading for meaning (is the refusal for the rule's own reason,
# does the permit sit at the boundary) is the judge's, next. A case's rule is the folder it sits in, so the
# floor also requires test.sh to assert an event of THAT rule.
# Contract: stdin is the Changeset payload; exit 1 with {"reason": ...} refuses; whatever cannot be read
# is refused.
set -uo pipefail
lib_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
unset rule_tests_rigorous_lib_loaded
. "$lib_dir/lib.sh" || exit 2
[ "${rule_tests_rigorous_lib_loaded:-}" = 1 ] || exit 2
payload="$(cat)"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

[ "$(printf '%s' "$payload" | jq -r '.event.kind // ""')" = "Changeset" ] ||
  refuse "expected a Changeset event, so the sr-test case could not be checked"
[ -n "${SR_TREE:-}" ] || refuse "SR_TREE is unset, so the committed sr-test case could not be read"
case_dir="$(printf '%s' "$payload" | jq -r '.subject.id // ""')" || case_dir=""
[ -n "$case_dir" ] || refuse "the changeset names no sr-test case folder, so no case could be checked"

abs="$SR_TREE/$case_dir"
test_sh="$abs/test.sh"
[ -f "$test_sh" ] ||
  refuse "$case_dir has no test.sh. A case is its test.sh (setup, run the agent, assert on the events); add it, or delete the folder."

problems=""
add() { problems="$problems
- $1"; }

first="$(head -n 1 "$test_sh")"
case "$first" in
  '#!'*) ;;
  *) add "test.sh does not start with a shebang line. Make line 1 '#!/usr/bin/env bash'." ;;
esac

# the body without comment lines: a comment that explains an assertion is neither one nor a violation
body="$(grep -v -E '^[[:space:]]*#' "$test_sh")"

if ! printf '%s\n' "$body" | grep -q -E '[.]events|SR_EVENTS_FILE' ||
  ! printf '%s\n' "$body" | grep -q -E 'jq[[:space:]]+(-[A-Za-z]*e[A-Za-z]*|--exit-status)([[:space:]]|$)'; then
  add "test.sh never asserts on the events (a 'jq -e' over '.events' of the sr-test agent result, or over \$SR_EVENTS_FILE). The exit code of sr-test agent only says the agent ran; assert which rule decided, with which outcome."
fi

# the owner's events: the case's folder names its rule (bare in the project, <plugin>/<rule> in a plugin) and
# its nature names the event kind; one jq assertion must carry both. Backslash continuations are joined first
# so an assertion split over lines is read as one.
case_split "$case_dir" ||
  refuse "$case_dir is not a case folder: a case lives in its rule's folder, .sloprail/<gate|file-guard|context>/<rule>/tests/<case>/ (the structure gate's in .sloprail/file-guard/structure.tests/<case>/)."
ev_rule="$(owner_rule "$CASE_ROOT" "$CASE_RULE")"
ev_kind="$(owner_kind "$CASE_NATURE")"
ev_re="$ev_rule"
# the structure gate is one file: a plugin's case for it builds the project that holds the structure.yaml, so its
# events carry the bare "structure" as well as "<plugin>/structure"
[ "$CASE_NATURE" != structure ] || ev_re="(${ev_rule%structure})?structure"
joined="$(printf '%s\n' "$body" | awk '{ if (sub(/\\$/, "")) { buf = buf $0; next } print buf $0; buf = "" } END { if (buf != "") print buf }')"
if ! printf '%s\n' "$joined" | grep -F -e "$ev_kind" | grep -E 'jq[[:space:]]' | grep -q -E "[.]rule[[:space:]]*==[[:space:]]*\"$ev_re\""; then
  add "test.sh asserts no event of its owning rule $ev_rule. The case sits in the folder of $CASE_NATURE/$CASE_RULE, so it must prove that rule: one jq -e must select '.kind==\"$ev_kind\" and .rule==\"$ev_rule\"' and assert its outcome. Events of other rules, or of another kind, do not count."
fi

if printf '%s\n' "$body" | grep -q 'refused'; then
  if ! printf '%s\n' "$body" | grep -q -E '[.]reason[^|]*[|][[:space:]]*(contains|test|startswith|endswith|index|inside|match)\(|[.]reason[[:space:]]*==|[.]reason[[:space:]]*\|[[:space:]]*(contains|test)'; then
    add "test.sh asserts a refusal but never checks its reason. Assert '(.reason|contains(\"...\"))' (or test(...)) with the sentence that names why the rule refused, so a refusal for some other reason fails."
  fi
fi

if printf '%s\n' "$body" | grep -q -E '[|][|][[:space:]]*(true|:|exit 0)[[:space:]]*($|[;)&}])'; then
  add "test.sh swallows a failure with '|| true' (or '|| :', '|| exit 0'). Remove it: an assertion that cannot fail proves nothing."
fi
if printf '%s\n' "$body" | grep -q -E '^[[:space:]]*(true|:)[[:space:]]*$'; then
  add "test.sh has a line that is only 'true' (or ':'). It asserts nothing; remove it or replace it with a real assertion."
fi
if printf '%s\n' "$body" | grep -q -E '\[[[:space:]]+(1|"1"|true|0)[[:space:]]+\]|\[[[:space:]]+(1[[:space:]]+-eq[[:space:]]+1|0[[:space:]]+-eq[[:space:]]+0)[[:space:]]+\]'; then
  add "test.sh has a test on a constant ('[ 1 ]', '[ 1 -eq 1 ]'). It can never fail; assert on the events instead."
fi
if printf '%s\n' "$body" | grep -q -E "jq[[:space:]]+(-[A-Za-z-]+[[:space:]]+)*['\"]?(\\.|true|1)['\"]?[[:space:]]*([>|;]|\$)"; then
  add "test.sh runs jq with a constant filter ('.', 'true', '1'). It passes on any input; filter the events and test a condition."
fi

# a mock judge that prints a fixed verdict decides nothing: it must read the judge's input on stdin
for f in "$abs"/judge*.sh; do
  [ -f "$f" ] || continue
  name="$(basename "$f")"
  if ! grep -v -E '^[[:space:]]*(#|echo|printf)' "$f" | grep -q -E '(^|[^[:alnum:]_-])(cat|read|grep|jq|sed|awk|head|tail|tr|wc|python3?)([[:space:]]|$)'; then
    add "$name is a mock judge that never reads its stdin, so it returns the same verdict whatever the input. Make it decide from the input (cat the stdin, grep it for what the rule is about) and print {\"pass\":...,\"reasoning\":...} accordingly."
  fi
done

if [ -n "$problems" ]; then
  refuse "$case_dir is not a rigorous sr-test case:$problems"
fi
exit 0
