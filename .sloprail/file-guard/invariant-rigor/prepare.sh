#!/usr/bin/env bash
# prepare: the invariant this check's subject names (subjects.sh; all the invariants touched when
# the rule runs unsplit), as additionalContext.subjects. Touched: a changed
# spec/<domain>/invariants/<id>.yaml, or a changed file carrying (before or after)
# sr:proves <id>. Each subject carries the statement (the predicate)
# (a short string) and the project path of every test file proving it; the judge
# reads the tests, they are not inlined.
set -uo pipefail
payload="$(cat)"
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/changeset.sh"
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/spec.sh"
load_spec invariants; inv="$SPEC"
load_markers proves; proves="$MARKERS"
ids="$( { cs '.changeset.files[].path | capture("^spec/(?<d>[a-z0-9-]+)/invariants/(?<n>[a-z0-9-]+)\\.yaml$") | "\(.d)/\(.n)"'
          cs '.changeset.files[] | ((.newMarkers // []) + (.oldMarkers // []))[] | select(.kind == "proves") | .fqn | select(test("^[a-z0-9-]+/[a-z0-9-]+$"))'
        } | sort -u)"
subjects="[]"
while IFS= read -r id; do
  [ -n "$id" ] || continue
  want_subject "$id" || continue
  st="$(jq -r --arg id "$id" '[.[] | select(.id == $id)][0].doc.predicate // empty' <<<"$inv")"
  [ -n "$st" ] || continue                     # removed, or malformed: invariant-covered's finding
  tests="$(printf '%s\n' "$proves" | awk -F'\t' -v id="$id" '$2 == id {print $1}' | sort -u | jq -R . | jq -sc 'map(select(. != ""))')"
  subjects="$(jq -c --arg id "$id" --arg st "$st" --argjson t "$tests" --arg p "spec/${id%%/*}/invariants/${id#*/}.yaml" \
    '. + [{id: $id, path: $p, statement: $st, tests: $t}]' <<<"$subjects")"
done <<<"$ids"
if [ "$(jq 'length' <<<"$subjects")" -eq 0 ]; then echo '{"skip": true}'; exit 0; fi
jq -n -c --argjson s "$subjects" '{additionalContext: {subjects: $s}}'
