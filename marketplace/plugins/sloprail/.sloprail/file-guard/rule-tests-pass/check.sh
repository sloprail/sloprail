#!/usr/bin/env bash
# rule-tests-pass: run every sr-test case of each `.sloprail` root the change touches, and require a case
# to exercise each rule the change added or edited. Reads the committed tree (SR_TREE). A rule nobody
# touched is not refused for having no case (legacy pass): only the changed rules go to `sr-test doctor`.
# Contract: stdin is the Changeset payload; exit 1 with {"reason": ...} refuses; whatever cannot run is refused.
#
# sr-test is given each touched root as an explicit path argument. When sr-test finds nested
# `**/.sloprail/tests` itself, this can become one run from the repo root.
set -uo pipefail
dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
. "$dir/lib.sh"
payload="$(cat)"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

[ "$(printf '%s' "$payload" | jq -r '.event.kind // ""')" = "Changeset" ] ||
  refuse "expected a Changeset event, so the sr-test cases could not be run"
[ -n "${SR_TREE:-}" ] || refuse "SR_TREE is unset, so the committed sr-test cases could not be run"
command -v sr-test >/dev/null 2>&1 ||
  refuse "sr-test is not on PATH, so the cases could not be run. Install sloprail's binaries (make build) and commit again."
printf '%s' "$payload" | jq -e '.changeset.files | type == "array"' >/dev/null 2>&1 ||
  refuse "the changeset's files could not be read, so the sr-test cases could not be run"

paths="$(printf '%s' "$payload" | jq -r '.changeset.files[].path')" ||
  refuse "the changeset's files could not be read, so the sr-test cases could not be run"

work="$(mktemp -d)" || refuse "could not make a scratch directory to run the sr-test cases"
trap 'rm -rf "$work"' EXIT

# 1. every case of every touched root
failures=""
while IFS= read -r root; do
  [ -n "$root" ] || continue
  abs="$SR_TREE"
  [ "$root" = "." ] || abs="$SR_TREE/$root"
  [ -d "$abs/.sloprail/tests" ] || continue
  out="$work/run.jsonl"
  err="$work/run.err"
  sr-test run --jobs 8 "$abs" >"$out" 2>"$err"
  status=$?
  bad="$(jq -r 'select(.status != "pass") | "- " + .subject + ": " + .status + "\n" + ((.output // "") | split("\n") | .[-12:] | map("    " + .) | join("\n"))' "$out" 2>/dev/null)"
  if [ -n "$bad" ]; then
    failures="$failures
$root/.sloprail/tests:
$bad"
  elif [ "$status" -ne 0 ]; then
    failures="$failures
$root/.sloprail/tests: sr-test run exited $status and reported no case:
$(tail -n 12 "$err" | sed 's/^/    /')"
  fi
done < <(printf '%s\n' "$paths" | roots_of)

if [ -n "$failures" ]; then
  refuse "sr-test cases fail after this change. Fix the rule or the case and commit again; run 'sr-test run <dir holding .sloprail>' to see them.$failures"
fi

# 2. a rule this change adds or edits must be exercised by some case
changed="$(printf '%s\n' "$paths" | awk '{
  p = "/" $0
  i = index(p, "/.sloprail/")
  if (i == 0) next
  root = substr(p, 2, i - 2); if (root == "") root = "."
  n = split(substr(p, i + 11), a, "/")
  if (n >= 3 && (a[1] == "file-guard" || a[1] == "gate" || a[1] == "context")) print root "\t" a[1] "\t" a[2]
}' | LC_ALL=C sort -u)"

missing=""
while IFS="	" read -r root nature rule; do
  [ -n "$rule" ] || continue
  abs="$SR_TREE"
  [ "$root" = "." ] || abs="$SR_TREE/$root"
  # only a rule that still stands: a deleted rule has no declaration at head
  [ -f "$abs/.sloprail/$nature/$rule/$nature.yaml" ] || continue
  printf '%s\t%s\t%s\n' "$root" "$nature" "$rule" >>"$work/rules.tsv"
done <<<"$changed"

if [ -s "$work/rules.tsv" ]; then
  while IFS= read -r root; do
    awk -F'\t' -v r="$root" '$1 == r' "$work/rules.tsv" | grep -q . || continue
    abs="$SR_TREE"
    [ "$root" = "." ] || abs="$SR_TREE/$root"
    sr-test doctor --jobs 8 "$abs" >"$work/doctor.out" 2>"$work/doctor.err"
    if ! grep -q -E '^(uncovered: |every rule is covered)' "$work/doctor.out"; then
      refuse "sr-test doctor could not tell which rules the cases exercise under $root/.sloprail:
$(tail -n 12 "$work/doctor.err" | sed 's/^/    /')"
    fi
    while IFS="	" read -r r nature rule; do
      [ "$r" = "$root" ] || continue
      if grep -q -E "^uncovered: $nature:(.*/)?$rule\$" "$work/doctor.out"; then
        missing="$missing
- $nature/$rule (under $root/.sloprail)"
      fi
    done <"$work/rules.tsv"
  done < <(cut -f1 "$work/rules.tsv" | LC_ALL=C sort -u)
fi

if [ -n "$missing" ]; then
  refuse "no sr-test case exercises these rules this change added or edited. Add a case under .sloprail/tests/<case>/ that makes each rule refuse and permit (see the authoring-guardrails skill), and commit it:$missing"
fi
exit 0
