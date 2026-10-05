#!/usr/bin/env bash
# rule-tests-pass, one subject = one `.sloprail` root the change touches (subject.id is its dir relative to
# the tree, "." for the repo root; subject.files the changed paths under its `.sloprail/`). Reads the
# committed tree (SR_TREE).
#   1. Scope. If the change touches ONLY files of cases (<nature>/<rule>/tests/<case>/ or
#      structure.tests/<case>/), only those cases run; if ANY other file of the root changed (a rule, the
#      config, the structure), ALL the root's cases run, since a rule edit can break a case nobody touched.
#      A case deleted in the range is not run.
#   2. Coverage. A rule this change added or changed (a <nature>/<rule>/ folder it touches, structure.yaml)
#      that still stands must have at least one case (`sr-test doctor`). A rule nobody touched is not refused
#      for having no case (legacy pass).
# Contract: stdin is the Changeset payload; exit 1 with {"reason": ...} refuses; whatever cannot run is refused.
# A refusal that is a verdict on the change (a case fails, a rule has no case) is cached for the same content. A
# refusal because sr-test itself could not do its job (missing, a case in status error, doctor or a run that
# reports nothing) carries "error": true: still refused, never cached, so the next run tries again.
set -uo pipefail
lib_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
unset rule_tests_pass_lib_loaded
. "$lib_dir/lib.sh" || exit 2
[ "${rule_tests_pass_lib_loaded:-}" = 1 ] || exit 2
payload="$(cat)"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

# refuse_error: the tooling failed, not the change. Refused, but no verdict: the engine does not cache it.
refuse_error() {
  jq -n --arg r "$1" '{reason: $r, error: true}'
  exit 1
}

[ "$(printf '%s' "$payload" | jq -r '.event.kind // ""')" = "Changeset" ] ||
  refuse "expected a Changeset event, so the sr-test cases could not be run"
[ -n "${SR_TREE:-}" ] || refuse "SR_TREE is unset, so the committed sr-test cases could not be run"
# sr-test: on PATH, else beside sr-checks (they ship together), else in the checkout's bin/
if ! command -v sr-test >/dev/null 2>&1; then
  for d in "$(dirname "$(command -v sr-checks 2>/dev/null || echo /nonexistent/x)")" "${SR_WORKSPACE:-/nonexistent}/bin"; do
    if [ -x "$d/sr-test" ]; then
      PATH="$d:$PATH"
      export PATH
      break
    fi
  done
fi
command -v sr-test >/dev/null 2>&1 ||
  refuse_error "sr-test is not on PATH, so the cases could not be run. Install sloprail's binaries (make build) and commit again."
printf '%s' "$payload" | jq -e '.changeset.files | type == "array"' >/dev/null 2>&1 ||
  refuse "the changeset's files could not be read, so the sr-test cases could not be run"

root="$(printf '%s' "$payload" | jq -r '.subject.id // ""')"
[ -n "$root" ] || refuse "the changeset names no .sloprail root, so the sr-test cases could not be chosen"
paths="$(printf '%s' "$payload" | jq -r '.subject.files[]?')" ||
  refuse "the subject's files could not be read, so the sr-test cases could not be chosen"

# Where the cases run. The committed tree (SR_TREE) is what is judged; but a plugin's cases call `sr-test agent`,
# which installs the plugin and the core plugin from the checkout it sits in, and that collides with a
# snapshot of the same plugin. So when the workspace IS the judged commit and has no local change, run there:
# the same bytes, in the checkout sr-test expects. Otherwise run the snapshot.
tree="$SR_TREE"
if [ -n "${SR_WORKSPACE:-}" ] && [ -n "${SR_HEAD:-}" ] &&
  [ "$(git -C "$SR_WORKSPACE" rev-parse HEAD 2>/dev/null)" = "$SR_HEAD" ] &&
  [ -z "$(git -C "$SR_WORKSPACE" status --porcelain 2>/dev/null)" ]; then
  tree="$SR_WORKSPACE"
fi
abs="$tree"
[ "$root" = "." ] || abs="$tree/$root"
sloprail="$abs/.sloprail"

work="$(mktemp -d)" || refuse_error "could not make a scratch directory to run the sr-test cases"
trap 'rm -rf "$work"' EXIT

printf '%s\n' "$paths" | classify "$root" >"$work/touched.tsv"

# 1. which cases run: owner:case subjects, as sr-test names them for this root
: >"$work/want.txt"
if grep -q -E '^other$' "$work/touched.tsv"; then
  scope="all the cases of $root/.sloprail (a rule, the config or the structure changed)"
  # every case of THIS root's .sloprail: <nature>/<rule>/tests/<case>/test.sh and file-guard/structure.tests/<case>/test.sh
  (cd "$sloprail" 2>/dev/null && find . -type f -name test.sh | sed 's|^\./||' | LC_ALL=C sort) | awk -F/ '
    NF == 5 && $3 == "tests" && ($1 == "gate" || $1 == "file-guard" || $1 == "context") && $2 != "structure.tests" { print $1 "/" $2 ":" $4 }
    NF == 4 && $1 == "file-guard" && $2 == "structure.tests" { print "file-guard/structure:" $3 }' >"$work/want.txt"
else
  scope="the cases the change touches, in $root/.sloprail (only tests changed)"
  # a case the range deleted is gone at head: skipped
  while IFS="	" read -r kind dir subject; do
    [ "$kind" = case ] || continue
    [ -f "$sloprail/$dir/test.sh" ] || continue
    printf '%s\n' "$subject"
  done <"$work/touched.tsv" | LC_ALL=C sort -u >"$work/want.txt"
fi

failures=""
tool_error=""
if [ -s "$work/want.txt" ]; then
  only=()
  while IFS= read -r s; do only+=(--only "$s"); done <"$work/want.txt"
  # --only is a substring filter: it can pick more cases than asked (a longer name, a nested .sloprail's). The
  # results are read for the exact subjects only.
  sr-test run "${only[@]}" "$abs" >"$work/run.jsonl" 2>"$work/run.err"
  status=$?
  want_json="$(jq -R -s -c 'split("\n") | map(select(length > 0))' "$work/want.txt")"
  failures="$(jq -r -s --argjson want "$want_json" '. as $r | $want[] as $s
    | ([$r[] | select(.subject == $s)] | first) as $x
    | if $x == null then "- " + $s + ": not run"
      elif $x.status != "pass" then "- " + $s + ": " + $x.status + "\n" + (($x.output // "") | split("\n") | .[-12:] | map("    " + .) | join("\n"))
      else empty end' "$work/run.jsonl" 2>/dev/null)"
  if [ -z "$failures" ] && ! jq -e -s 'length > 0' "$work/run.jsonl" >/dev/null 2>&1; then
    failures="sr-test run exited $status and reported no case:
$(tail -n 12 "$work/run.err" | sed 's/^/    /')"
    tool_error=1
  fi
  # a wanted case that did not run, or ran to status error (sr-test could not run it: exit 2, a timeout, a setup
  # failure), is sr-test failing, not the change failing a case
  if jq -e -s --argjson want "$want_json" '. as $r | any($want[] as $s | ([$r[] | select(.subject == $s)] | first) as $x | $x == null or $x.status == "error")' "$work/run.jsonl" >/dev/null 2>&1; then
    tool_error=1
  fi
fi
if [ -n "$failures" ]; then
  [ "${tool_error:-}" = 1 ] && refuse_error "sr-test could not run its cases (ran $scope); this is no verdict on the change, so it is tried again on the next run. Run 'sr-test run $root' to see why.
$failures"
  refuse "sr-test cases fail after this change (ran $scope). Fix the rule or the case and commit again; run 'sr-test run $root' to see them.
$failures"
fi

# 2. a rule this change adds or changes must have a case. Only rules that still stand: a deleted rule has no declaration at head.
missing=""
shown=".sloprail"
[ "$root" = "." ] || shown="$root/.sloprail"
if grep -q -E '^rule	' "$work/touched.tsv"; then
  # one JSON object per uncovered rule, each with the .sloprail it sits in (dir, "." for this root's own):
  # the rule is matched on (dir, nature, rule), never on a name rebuilt here from plugin.json
  if ! sr-test doctor --json "$abs" >"$work/doctor.out" 2>"$work/doctor.err" ||
    ! jq -e -s 'all(.[]; type == "object" and (.dir | type == "string") and (.nature | type == "string") and (.rule | type == "string"))' "$work/doctor.out" >/dev/null 2>&1; then
    refuse_error "sr-test doctor could not tell which rules have a case under $root/.sloprail:
$(tail -n 12 "$work/doctor.err" | sed 's/^/    /')"
  fi
  while IFS="	" read -r kind nature rule; do
    [ "$kind" = rule ] || continue
    if [ "$nature" = structure ]; then
      decl="$sloprail/file-guard/structure.yaml"; label="file-guard/structure"; casedir="$shown/file-guard/structure.tests/<case>/"
    else
      decl="$sloprail/$nature/$rule/$nature.yaml"; label="$nature/$rule"; casedir="$shown/$nature/$rule/tests/<case>/"
    fi
    [ -f "$decl" ] || continue
    if jq -e -s --arg n "$nature" --arg r "$rule" 'any(.[]; .dir == "." and .nature == $n and .rule == $r)' "$work/doctor.out" >/dev/null 2>&1; then
      missing="$missing
- $label: add a case in $casedir"
    fi
  done < <(LC_ALL=C sort -u "$work/touched.tsv")
fi
if [ -n "$missing" ]; then
  refuse "no sr-test case lives in the folder of these rules this change added or edited. A case sits in its owning rule's folder; add one that makes the rule refuse and permit (see the authoring-guardrails skill), and commit it:$missing"
fi
exit 0
