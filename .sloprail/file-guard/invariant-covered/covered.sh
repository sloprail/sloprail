#!/usr/bin/env bash
# The file's shape is file-guard/shapes' (schemas/invariant.cue). For each
# spec/invariants/<id>.yaml:
#   - ≥1 `// sr:invariant <id>` in non-test code
#   - ≥1 `// sr:proves <id>` in a *_test.go
# And back: every sr:invariant, and every sr:proves without a /harness part,
# names an invariant; sr:proves sits only in tests.
set -uo pipefail
payload="$(cat)"
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/changeset.sh"
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/spec.sh"
load_spec invariants; inv="$SPEC"
load_markers invariant; impl="$MARKERS"
load_markers proves; proves="$(printf '%s\n' "$MARKERS" | awk -F'\t' 'NF && $2 !~ /\//')"

problems=""
add() { problems="${problems}- $1"$'\n'; }
ids="$(jq -r '.[].id' <<<"$inv")"
while IFS= read -r i; do
  [ -n "$i" ] || continue
  id="$(jq -r '.id' <<<"$i")"
  kebab "$id" || add "spec/invariants/$id.yaml: the file name must be kebab-case"
  printf '%s\n' "$impl" | awk -F'\t' -v id="$id" '$2 == id && $1 !~ /_test\.go$/' | grep -q . ||
    add "invariant '$id' has no implementation: mark the code that upholds it with // sr:invariant $id"
  printf '%s\n' "$proves" | awk -F'\t' -v id="$id" '$2 == id && $1 ~ /_test\.go$/' | grep -q . ||
    add "invariant '$id' has no test: mark a test that proves it with // sr:proves $id"
done < <(jq -c '.[]' <<<"$inv")
while IFS=$'\t' read -r path id; do
  [ -n "$path" ] || continue
  printf '%s\n' "$ids" | grep -Fxq -- "$id" || add "$path: sr:invariant '$id' names no spec/invariants/$id.yaml"
done <<<"$impl"
while IFS=$'\t' read -r path id; do
  [ -n "$path" ] || continue
  printf '%s\n' "$ids" | grep -Fxq -- "$id" || add "$path: sr:proves '$id' names no spec/invariants/$id.yaml"
  case "$path" in *_test.go) ;; *) add "$path: sr:proves belongs on a test, in a *_test.go" ;; esac
done <<<"$proves"
[ -z "$problems" ] && exit 0
refuse "Invariants not implemented or not proven:
${problems}"
