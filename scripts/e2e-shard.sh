#!/usr/bin/env bash
# Prints (or, with `run`, runs) the e2e work that belongs to shard INDEX of
# COUNT (1-based) out of the packages matching PATTERN...
#
#   scripts/e2e-shard.sh 2 6 list ./tests/e2e/harness/session/...
#   scripts/e2e-shard.sh 2 6 run  ./tests/e2e/harness/session/...
#
# A unit is one line: an import path (a whole package), or an import path, a
# TAB and a -run regex (a slice of a package too slow to be one unit).
#
# The partition is DISCOVERED on every call, never hand-listed:
#   - packages come from `go list PATTERN...`, so a new package lands in a
#     shard with no edit here;
#   - a package marked `split` in e2e-shard-weights.txt is cut test by test
#     (top-level Test functions read from its *_test.go), so a new test in it
#     lands in some shard with no edit here either;
#   - items are packed greedily, heaviest first, into COUNT bins by the
#     MEASURED seconds in e2e-shard-weights.txt. An unmeasured package or test
#     gets a default weight. A stale weight costs balance, never coverage.
# The output is deterministic, so every shard sees the same partition. The
# guard in tests/repo runs this for every shard and fails if any package or
# any test of a split package is in no shard.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
weights="$here/e2e-shard-weights.txt"

index="${1:?usage: e2e-shard.sh INDEX COUNT list|run PATTERN...}"
count="${2:?usage: e2e-shard.sh INDEX COUNT list|run PATTERN...}"
mode="${3:?usage: e2e-shard.sh INDEX COUNT list|run PATTERN...}"
shift 3
[ "$#" -gt 0 ] || { echo "no package pattern given" >&2; exit 2; }
case "$index$count" in *[!0-9]*) echo "INDEX and COUNT must be integers" >&2; exit 2 ;; esac
if [ "$index" -lt 1 ] || [ "$index" -gt "$count" ]; then
  echo "INDEX must be in 1..COUNT" >&2
  exit 2
fi

DEFAULT_PKG=15
DEFAULT_TEST=3

# weight KEY DEFAULT: the measured seconds for KEY, else DEFAULT.
weight() {
  awk -v k="$1" -v d="$2" '$1 == k { print $2; found = 1; exit } END { if (!found) print d }' "$weights"
}

is_split() {
  awk -v k="$1" '$1 == "split" && $2 == k { found = 1 } END { exit !found }' "$weights"
}

items() {
  local ip dir key tests name
  go list -f '{{.ImportPath}} {{.Dir}}' "$@" | while read -r ip dir; do
    key="${ip#*/tests/e2e/}"
    if is_split "$key"; then
      tests="$(grep -hoE '^func Test[A-Za-z0-9_]*\(' "$dir"/*_test.go | sed -E 's/^func (.*)\($/\1/' | grep -vx TestMain | sort -u)"
      while IFS= read -r name; do
        [ -n "$name" ] || continue
        printf '%s\t%s\t%s\n' "$(weight "$key:$name" "$DEFAULT_TEST")" "$ip" "$name"
      done <<<"$tests"
    else
      printf '%s\t%s\t\n' "$(weight "$key" "$DEFAULT_PKG")" "$ip"
    fi
  done
}

all="$(items "$@")"
[ -n "$all" ] || { echo "no packages found for: $*" >&2; exit 1; }

# Pack: heaviest first, each into the lightest bin; keep mine, one line per
# package with its tests joined into one regex.
mine="$(printf '%s\n' "$all" | LC_ALL=C sort -t"$(printf '\t')" -k1,1nr -k2,2 -k3,3 \
  | awk -F'\t' -v n="$count" -v want="$index" '
      BEGIN { for (i = 1; i <= n; i++) load[i] = 0 }
      {
        best = 1
        for (i = 2; i <= n; i++) if (load[i] < load[best]) best = i
        load[best] += $1
        if (best != want) next
        if ($3 == "") whole[$2] = 1
        else { re[$2] = (($2 in re) ? re[$2] "|" : "") $3 }
      }
      END {
        for (p in whole) print p
        for (p in re) print p "\t^(" re[p] ")$"
      }' | LC_ALL=C sort)"

if [ "$mode" != run ]; then
  [ -z "$mine" ] || printf '%s\n' "$mine"
  exit 0
fi

# go test -v, for the per-test timings (`--- PASS: TestX (1.2s)`) that
# e2e-shard-weights.txt is measured from. Its full output is held back and
# printed only when the package fails: streaming it live made a test that logs
# a 650KB payload take minutes in CI, and a green run needs the timings, not
# the chatter.
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
rc=0
while IFS="$(printf '\t')" read -r ip re; do
  [ -n "$ip" ] || continue
  args=(-v -p 1 -count=1 -timeout 30m)
  [ -z "$re" ] || args+=(-run "$re")
  if go test "${args[@]}" "$ip" >"$tmp" 2>&1; then
    grep -E '^(--- |ok  |PASS$)' "$tmp" || true
  else
    cat "$tmp"
    rc=1
  fi
done <<<"$mine"
exit "$rc"
