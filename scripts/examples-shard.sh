#!/usr/bin/env bash
# Prints (or, with a third argument `run`, runs) the example e2e work that
# belongs to shard INDEX of COUNT (1-based).
#
#   scripts/examples-shard.sh 2 6          # list the units of shard 2
#   scripts/examples-shard.sh 2 6 run      # go test them
#
# A unit is one line: an import path (a whole package), or an import path, a
# TAB and a -run regex (a slice of a package too slow to be one unit).
#
# The partition is DISCOVERED on every call, never hand-listed:
#   - packages come from `go list ./tests/e2e/examples/...`, so a new example
#     package lands in a shard with no edit here;
#   - a package named in SPLIT is cut into that many slices by a hash of each
#     top-level test name (read from the package's *_test.go), so a new test
#     in it lands in some slice with no edit here either;
#   - slices and packages are then packed greedily, heaviest first, into COUNT
#     bins by WEIGHT (seconds, measured from CI). An unmeasured package gets
#     DEFAULT_WEIGHT. A stale weight costs balance, never coverage.
# The output is deterministic, so every shard sees the same partition. The
# guard in tests/repo runs this for every shard and fails if any package or
# any test of a split package is in no shard.
set -euo pipefail

index="${1:?usage: examples-shard.sh INDEX COUNT [run]}"
count="${2:?usage: examples-shard.sh INDEX COUNT [run]}"
mode="${3:-list}"
case "$index$count" in *[!0-9]*) echo "INDEX and COUNT must be integers" >&2; exit 2 ;; esac
if [ "$index" -lt 1 ] || [ "$index" -gt "$count" ]; then
  echo "INDEX must be in 1..COUNT" >&2
  exit 2
fi

DEFAULT_WEIGHT=15

# Measured package wall times in seconds (CI run 36837249037).
weight() {
  case "$1" in
    */046_business_invariants)        echo 343 ;;
    */039_research_rigor)             echo 207 ;;
    */038_keyword_coverage_registry)  echo 141 ;;
    */049_no_unasked_deletion)        echo 36 ;;
    */036_intake_nothing_unprocessed) echo 29 ;;
    *) echo "$DEFAULT_WEIGHT" ;;
  esac
}

# Packages cut into this many slices by test-name hash.
splits() {
  case "$1" in
    */046_business_invariants) echo 3 ;;
    */039_research_rigor)      echo 2 ;;
    *) echo 1 ;;
  esac
}

units() {
  local ip dir n w tests name k parts re
  go list -f '{{.ImportPath}} {{.Dir}}' ./tests/e2e/examples/... | while read -r ip dir; do
    n="$(splits "$ip")"
    w="$(weight "$ip")"
    if [ "$n" -le 1 ]; then
      printf '%s\t%s\t\n' "$w" "$ip"
      continue
    fi
    tests="$(grep -hoE '^func Test[A-Za-z0-9_]*\(' "$dir"/*_test.go | sed -E 's/^func (.*)\($/\1/' | grep -vx TestMain | sort -u)"
    for ((k = 0; k < n; k++)); do
      parts=""
      while IFS= read -r name; do
        [ -n "$name" ] || continue
        if [ $(($(printf '%s' "$name" | cksum | cut -d' ' -f1) % n)) -eq "$k" ]; then
          parts="${parts:+$parts|}$name"
        fi
      done <<<"$tests"
      [ -n "$parts" ] || continue
      re="^($parts)\$"
      printf '%s\t%s\t%s\n' "$((w / n))" "$ip" "$re"
    done
  done
}

all="$(units)"
[ -n "$all" ] || { echo "no example packages found" >&2; exit 1; }

mine="$(printf '%s\n' "$all" | sort -t$'\t' -k1,1nr -k2,2 -k3,3 \
  | awk -F'\t' -v n="$count" -v want="$index" '
      BEGIN { for (i = 1; i <= n; i++) load[i] = 0 }
      {
        best = 1
        for (i = 2; i <= n; i++) if (load[i] < load[best]) best = i
        load[best] += $1
        if (best == want) { if ($3 == "") print $2; else print $2 "\t" $3 }
      }')"

if [ "$mode" != run ]; then
  [ -z "$mine" ] || printf '%s\n' "$mine"
  exit 0
fi

rc=0
while IFS=$'\t' read -r ip re; do
  [ -n "$ip" ] || continue
  if [ -n "$re" ]; then
    go test -p 1 -count=1 -timeout 30m -run "$re" "$ip" || rc=1
  else
    go test -p 1 -count=1 -timeout 30m "$ip" || rc=1
  fi
done <<<"$mine"
exit "$rc"
