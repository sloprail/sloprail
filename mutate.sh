#!/bin/bash
# Mutation harness for internal/commandmod.
#
# The discipline this enforces: a mutant that does not COMPILE is not a kill.
# It is a broken edit, and scoring it as a kill is how a mutation number gets
# faked. Each mutant is applied, compiled, and only then run; the outcome is
# reported as COMPILE-FAIL (rewrite it), KILLED (with the tests that went red),
# or SURVIVED (a real gap).
set -uo pipefail
cd "$(dirname "$0")" || exit 1

FILE="$1"; FROM="$2"; TO="$3"; NAME="$4"
BAK=$(mktemp)
cp "$FILE" "$BAK"
restore() { cp "$BAK" "$FILE"; rm -f "$BAK"; }
trap restore EXIT

# Verify the pattern EXISTS before touching anything. An unapplied mutant that
# gets scored SURVIVED is how a mutation number gets faked: the code was never
# changed, so of course the tests passed. This happened once during this work —
# a pattern written with one tab where the source had two — and the guard below
# is what makes it impossible to repeat.
if ! grep -qF "$FROM" "$FILE"; then
  echo "NOT-APPLIED  $NAME  (pattern absent — the mutant is wrong, not the code)"
  exit 3
fi

# Apply with perl (literal, first occurrence only) so regex metachars in the
# Go source cannot silently change what is replaced.
perl -0777 -pi -e 'BEGIN{$f=shift;$t=shift} $done ||= s/\Q$f\E/$t/' "$FROM" "$TO" "$FILE"

# And verify it actually CHANGED. Belt and braces: the pattern was present, so
# the file must now differ from the backup.
if cmp -s "$FILE" "$BAK"; then
  echo "NOT-APPLIED  $NAME  (file unchanged after substitution)"
  exit 3
fi

if ! go vet ./internal/commandmod/ >/dev/null 2>&1; then
  echo "COMPILE-FAIL $NAME  (not a kill — rewrite this mutant)"
  exit 2
fi

OUT=$(go test -p 1 -count=1 ./internal/commandmod/ 2>&1)
if [ $? -eq 0 ]; then
  echo "SURVIVED     $NAME"
  exit 1
fi

KILLERS=$(echo "$OUT" | grep -oE '^--- FAIL: [A-Za-z_]+' | sed 's/--- FAIL: //' | sort -u | tr '\n' ' ')
echo "KILLED       $NAME  <- $KILLERS"
exit 0
