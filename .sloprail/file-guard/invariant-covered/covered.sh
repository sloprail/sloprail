#!/usr/bin/env bash
# The file's shape is file-guard/spec-quality's (spec.cue). For each invariant the change
# touches (its spec/<domain>/invariants/<id>.yaml changed, or a changed file carries, before
# or after, a marker naming it) that exists at head:
#   - ≥1 `// sr:invariant <domain>/<id>` in non-test code
#   - ≥1 `// sr:proves <domain>/<id>` in a test (is_test: *_test.go, tests/, a rule's sr-test case)
# Untouched invariants are not re-checked: what stood before this rule is taken as it is.
# And back, over the whole tree: every sr:invariant and sr:proves naming a <domain>/<id>
# names an invariant; sr:proves sits only in tests, sr:invariant only outside them.
set -uo pipefail
payload="$(cat)"
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/changeset.sh"
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/spec.sh"
load_spec invariants; inv="$SPEC"
load_markers invariant; impl="$MARKERS"
load_markers proves; proves="$MARKERS"
# Only our ids (<domain>/<id>): examples/ carry their own pinned-link markers.
impl="$(printf '%s\n' "$impl" | awk -F'\t' '$2 ~ /^[a-z0-9-]+\/[a-z0-9-]+$/')"
proves="$(printf '%s\n' "$proves" | awk -F'\t' '$2 ~ /^[a-z0-9-]+\/[a-z0-9-]+$/')"
TEST='(_test[.]go$|^tests/|(^|/)[.]sloprail/.*/tests/)'
touched="$(cs '.changeset.files[] | (.path | capture("^spec/(?<d>[a-z0-9-]+)/invariants/(?<n>[a-z0-9-]+)\\.yaml$") | "\(.d)/\(.n)"),
  (((.newMarkers // []) + (.oldMarkers // []))[] | select(.kind == "invariant" or .kind == "proves") | .fqn)' | sort -u)"

problems=""
add() { problems="${problems}- $1"$'\n'; }
ids="$(jq -r '.[].id' <<<"$inv")"
while IFS= read -r i; do
  [ -n "$i" ] || continue
  id="$(jq -r '.id' <<<"$i")"
  printf '%s\n' "$touched" | grep -Fxq -- "$id" || continue
  kebab "${id%%/*}" && kebab "${id#*/}" || add "$(jq -r '.path' <<<"$i"): the domain and file name must be kebab-case"
  printf '%s\n' "$impl" | awk -F'\t' -v id="$id" -v t="$TEST" '$2 == id && $1 !~ t' | grep -q . ||
    add "invariant '$id' has no implementation: mark the code that upholds it with // sr:invariant $id"
  printf '%s\n' "$proves" | awk -F'\t' -v id="$id" -v t="$TEST" '$2 == id && $1 ~ t' | grep -q . ||
    add "invariant '$id' has no test: mark a test that proves it with // sr:proves $id"
done < <(jq -c '.[]' <<<"$inv")
while IFS=$'\t' read -r path id; do
  [ -n "$path" ] || continue
  printf '%s\n' "$ids" | grep -Fxq -- "$id" || add "$path: sr:invariant '$id' names no spec/${id%%/*}/invariants/${id#*/}.yaml"
  [[ "$path" =~ $TEST ]] && add "$path: sr:invariant marks the code that upholds an invariant, not a test; use // sr:proves $id"
done <<<"$impl"
while IFS=$'\t' read -r path id; do
  [ -n "$path" ] || continue
  printf '%s\n' "$ids" | grep -Fxq -- "$id" || add "$path: sr:proves '$id' names no spec/${id%%/*}/invariants/${id#*/}.yaml"
  [[ "$path" =~ $TEST ]] || add "$path: sr:proves belongs on a test (a *_test.go, under tests/, or a rule's sr-test case)"
done <<<"$proves"
[ -z "$problems" ] && exit 0
refuse "Invariants not implemented or not proven:
${problems}"
