#!/usr/bin/env bash
# Every declared move must have landed. The declared scope is the paired
# context's payload (.context.refactoring.payload.declared_markers); `require`
# guarantees it's settled before this runs. Each token is the fqn a landed
# `sr:moved-from` marker carries, so "did this move land" is a literal search of
# the tree for that marker — the file-guard has already checked the bytes.
#
# Portability: no mapfile/readarray (bash 3.2 lacks them); sets are carried as
# newline-delimited strings walked with `while read`.
set -uo pipefail

input="$(cat)"

# Empty -> nothing declared -> permit (a missing scope must not be a false refusal).
declared="$(printf '%s' "$input" \
  | jq -r '.context.refactoring.payload.declared_markers[]? | select(. != "")' 2>/dev/null)"

if [ -z "$declared" ]; then
  exit 0
fi

root="${SR_WORKSPACE:-.}"

# grep -F: the fqn is a literal (contains . @ : -). Leader-agnostic; .git skipped.
missing=""
while IFS= read -r fqn; do
  [ -z "$fqn" ] && continue
  if ! grep -rIF --exclude-dir=.git -- "sr:moved-from $fqn" "$root" >/dev/null 2>&1; then
    if [ -z "$missing" ]; then
      missing="$fqn"
    else
      missing="$missing, $fqn"
    fi
  fi
done <<EOF
$declared
EOF

if [ -n "$missing" ]; then
  jq -n --arg detail "$missing" \
    '{reason: ("Refactor declared but not complete — these declared moves never landed as an sr:moved-from marker: " + $detail + ". Finish the moves you declared, or the turn cannot end.")}'
  exit 1
fi

exit 0
