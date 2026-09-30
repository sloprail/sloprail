#!/usr/bin/env bash
# prepare for stage 2 of task-body-is-human-authored: hand the judge the body it
# rules on. The cited words need no preparing: `asks` is the changeset's citations in
# the user pool (each quote with the whole message it came from). Receives the SAME
# CheckPayload stage 1 (body-is-stated.sh) did.
#
# SKIPS THE JUDGE when no grounding was required — a status/frontmatter-only change
# leaves every body byte-identical, and body-changed.sh waived the citation — so
# no model call is spent on a change that altered nothing the judge rules on. This
# is the file-guard's copy (committed bytes); the PreFileWrite gate's carries the
# pending copy.
#
# Output nests under `additionalContext` (the one key the engine reads from a
# prepare): .bodies (each changed task's path and the prose the judge rules on) and
# .asks (the changeset's citations in the user pool). `{"skip": true}` abstains. A non-zero exit fails the check closed.
set -uo pipefail

skip() { printf '{"skip": true}\n'; exit 0; }

fail() {
  echo "task-body-is-human-authored: $1" >&2
  exit 1
}

command -v jq >/dev/null 2>&1 || fail "jq is not on PATH, so the body could not be read"

payload="$(cat)"
[ "$(printf '%s' "$payload" | jq -r '.event.kind // ""' 2>/dev/null)" = "Changeset" ] ||
  fail "expected a Changeset event, so the cited messages could not be assembled"

lib="${SR_GUARDRAIL_DIR:-.}/lib-body.sh"
[ -f "$lib" ] || fail "lib-body.sh not found at $lib, so the cited messages could not be assembled"
# A helper stopped by a syntax error runs only up to it (whether the `.` then
# fails depends on the bash version); only its last-line sentinel proves it
# loaded whole.
unset lib_body_loaded
# shellcheck source=lib-body.sh
. "$lib"
[ "${lib_body_loaded:-}" = 1 ] ||
  fail "lib-body.sh did not load whole (its last-line sentinel lib_body_loaded is unset), so the cited messages could not be assembled"

n="$(printf '%s' "$payload" | jq -r '.changeset.files | length')" || fail "the changeset's files could not be read"

# Each task whose ask this changeset set: added, or its body changed.
bodies='[]'
i=0
while [ "$i" -lt "$n" ]; do
  idx="$i"
  i=$((i + 1))
  f() { printf '%s' "$payload" | jq -r --argjson i "$idx" ".changeset.files[\$i]$1"; }
  status="$(f '.status')" || fail "could not read file $idx of the changeset"
  path="$(f '.path')" || fail "could not read file $idx of the changeset"
  [ "$status" = "D" ] && continue
  content="$(f '.newContent')" || fail "could not read $path from the changeset"
  body="$(task_body "$content")"
  if [ "$status" != "A" ]; then
    old="$(f '.oldContent // ""')" || fail "could not read the earlier $path from the changeset"
    [ "$body" = "$(task_body "$old")" ] && continue
  fi
  bodies="$(printf '%s' "$bodies" | jq -c --arg path "$path" --arg body "$body" '. + [{path: $path, body: $body}]')" ||
    fail "could not assemble the body of $path"
done

# Grounding not required: no body changed, so there is nothing to judge.
[ "$bodies" = "[]" ] && skip

# .asks is the changeset's citations in the user pool only: the range's commits may
# also cite tool output (a later in_review move), which is not the ask, so the judge
# is never shown it as if it were.
printf '%s' "$payload" | jq --argjson bodies "$bodies" \
  '{additionalContext: {bodies: $bodies, asks: [(.changeset.citations // [])[] | select(((.sourceTypes // []) | index("user")) != null)]}}'
