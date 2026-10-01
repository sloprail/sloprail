#!/usr/bin/env bash
# Prints the example e2e packages that belong to shard INDEX of COUNT
# (1-based), one import path per line.
#
#   scripts/examples-shard.sh 2 3
#
# The partition is DISCOVERED: it is computed from `go list` on every call, so
# a new tests/e2e/examples/NNN_* package lands in a shard with no edit here,
# and never goes unrun. It is balanced by greedy longest-first bin packing on
# the weights below (seconds, measured from CI); a package with no entry gets
# DEFAULT_WEIGHT, so an unmeasured newcomer is placed sensibly and the split
# only drifts slowly. Re-measure and edit the table only when a package gets
# markedly heavier or lighter — a stale weight costs balance, never coverage.
# The result is deterministic (ties break by name), so every shard's
# invocation sees the same partition.
set -euo pipefail

index="${1:?usage: examples-shard.sh INDEX COUNT}"
count="${2:?usage: examples-shard.sh INDEX COUNT}"
case "$index$count" in *[!0-9]*) echo "INDEX and COUNT must be integers" >&2; exit 2 ;; esac
if [ "$index" -lt 1 ] || [ "$index" -gt "$count" ]; then
  echo "INDEX must be in 1..COUNT" >&2
  exit 2
fi

DEFAULT_WEIGHT=15

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

pkgs="$(go list ./tests/e2e/examples/...)"
[ -n "$pkgs" ] || { echo "no example packages found" >&2; exit 1; }

# weight<TAB>package, heaviest first, ties by name.
while IFS= read -r p; do printf '%s\t%s\n' "$(weight "$p")" "$p"; done <<<"$pkgs" \
  | sort -t$'\t' -k1,1nr -k2,2 \
  | awk -F'\t' -v n="$count" -v want="$index" '
      BEGIN { for (i = 1; i <= n; i++) load[i] = 0 }
      {
        best = 1
        for (i = 2; i <= n; i++) if (load[i] < load[best]) best = i
        load[best] += $1
        if (best == want) print $2
      }'
