#!/usr/bin/env bash
# rule-tests-pass, one subject = one RULE the change touches (subject.id is the rule's folder, see lib.sh;
# subject.files the changed paths whose content changed: the rule's own, a shared file of its root (lib/,
# schemas/), or a file of another rule it sources). Reads the committed tree (SR_TREE).
#   1. Cases. Runs `sr-test run . --rule <nature>/<rule>` in the rule's `.sloprail` root: every case of that
#      rule, and only that rule's, so a repo with many rules is many small cached runs, not one long one. A case
#      a change deleted is gone at head and is not run.
#   2. Coverage. A rule this change added or changed (a file of its own folder, structure.yaml) that still stands
#      must have at least one case (`sr-test doctor`). A rule nobody touched is not refused for having no case
#      (legacy pass).
# A subject whose files lists nothing (a mode-only change, same bytes) is no change of the rule: it passes.
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
# When one case fails and another errors, the whole refusal is an error (not cached): the real failure is
# reported too, and the next run, with sr-test working, settles both.
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

id="$(printf '%s' "$payload" | jq -r '.subject.id // ""')"
[ -n "$id" ] || refuse "the changeset names no rule, so the sr-test cases could not be chosen"
# a root's marker (a shared file of a root with no rule) has no case to run
if is_marker "$id"; then exit 0; fi
rule_split "$id" || refuse "the subject $id is no rule folder, so the sr-test cases could not be chosen"
nfiles="$(printf '%s' "$payload" | jq -r '.subject.files | length')" ||
  refuse "the subject's files could not be read, so the sr-test cases could not be chosen"
# a mode-only change is no change of the rule
[ "$nfiles" != 0 ] || exit 0

root="${CASE_ROOT:-.}"
nature="$CASE_NATURE"
rule="$CASE_RULE"
if [ "$nature" = structure ]; then owner="file-guard/structure"; else owner="$nature/$rule"; fi

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

# 1. the rule's cases, as sr-test names them (owner:case). A rule with no case standing has none to run.
rule_cases "$id" | while IFS= read -r c; do
  [ -n "$c" ] && printf '%s:%s\n' "$owner" "$(basename "$c")"
done >"$work/want.txt"

failures=""
tool_error=""
if [ -s "$work/want.txt" ]; then
  # run in the rule's root: the subjects are then named <owner>:<case>, with no nested-folder prefix
  (cd "$abs" && sr-test run . --rule "$owner") >"$work/run.jsonl" 2>"$work/run.err"
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
  [ "${tool_error:-}" = 1 ] && refuse_error "sr-test could not run the cases of $owner (ran 'sr-test run . --rule $owner' in $root); this is no verdict on the change, so it is tried again on the next run. Run it to see why.
$failures"
  refuse "sr-test cases of $owner fail after this change (ran 'sr-test run . --rule $owner' in $root). Fix the rule or the case and commit again.
$failures"
fi

# 2. a rule this change adds or changes must have a case, if it still stands (a deleted rule has no declaration at head)
shown=".sloprail"
[ "$root" = "." ] || shown="$root/.sloprail"
if [ "$nature" = structure ]; then
  decl="$sloprail/file-guard/structure.yaml"; label="file-guard/structure"; casedir="$shown/file-guard/structure.tests/<case>/"
else
  decl="$sloprail/$nature/$rule/$nature.yaml"; label="$nature/$rule"; casedir="$shown/$nature/$rule/tests/<case>/"
fi
# Only when a file of the rule itself changed: a shared file or a file another rule sources runs this rule's cases
# (above) but does not edit the rule, so a legacy rule with no case is not refused for it.
own_touched=""
while IFS= read -r -d '' f; do
  [ "$(path_subject "$f" 2>/dev/null)" = "$id" ] && own_touched=1
done < <(printf '%s' "$payload" | jq -j '.subject.files[] + "\u0000"')
if [ -f "$decl" ] && [ -n "$own_touched" ]; then
  # one JSON object per uncovered rule, each with the .sloprail it sits in (dir, "." for this root's own):
  # the rule is matched on (dir, nature, rule), never on a name rebuilt here from plugin.json
  if ! (cd "$abs" && sr-test doctor --json .) >"$work/doctor.out" 2>"$work/doctor.err" ||
    ! jq -e -s 'all(.[]; type == "object" and (.dir | type == "string") and (.nature | type == "string") and (.rule | type == "string"))' "$work/doctor.out" >/dev/null 2>&1; then
    refuse_error "sr-test doctor could not tell which rules have a case under $shown:
$(tail -n 12 "$work/doctor.err" | sed 's/^/    /')"
  fi
  if jq -e -s --arg n "$nature" --arg r "$rule" 'any(.[]; .dir == "." and .nature == $n and .rule == $r)' "$work/doctor.out" >/dev/null 2>&1; then
    refuse "no sr-test case lives in the folder of this rule this change added or edited. A case sits in its owning rule's folder; add one that makes the rule refuse and permit (see the authoring-guardrails skill), and commit it:
- $label: add a case in $casedir"
  fi
fi
exit 0
