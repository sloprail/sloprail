#!/usr/bin/env bash
# subjects: one per invariant touched: its spec file changed, or a changed file carries (before or
# after) sr:invariant <id>.
#   files        the changed files that touch it: its spec file, and the files so marked
#   fingerprint  what the judge reads beyond them: the spec file (the predicate) and every file
#                marked sr:invariant <id>, changed or not.
# A change to invariant A leaves invariant B's subject (files and fingerprint) as it was.
set -uo pipefail
payload="$(cat)"
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/changeset.sh"
slim_payload
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/subjects.sh"
load_markers invariant
arr="$(printf '%s' "$payload" | jq -c --arg proves "$MARKERS" '
  .changeset.files as $files
  | [$proves | split("\n")[] | select(length > 0) | split("\t") | {p: .[0], q: .[1]}] as $pv
  | ([$files[] | (.path | capture("^spec/(?<d>[a-z0-9-]+)/invariants/(?<n>[a-z0-9-]+)\\.yaml$") | "\(.d)/\(.n)"),
      (((.newMarkers // []) + (.oldMarkers // []))[] | select(.kind == "invariant") | .fqn | select(test("^[a-z0-9-]+/[a-z0-9-]+$")))] | unique)
    | map(. as $id | {id: $id,
        files: [$files[] | select(.path == "spec/\($id | split("/")[0])/invariants/\($id | split("/")[1]).yaml"
                 or any(((.newMarkers // []) + (.oldMarkers // []))[]; .kind == "invariant" and .fqn == $id)) | .path],
        deps: (["spec/\($id | split("/")[0])/invariants/\($id | split("/")[1]).yaml"] + [$pv[] | select(.q == $id) | .p])})')"
sub_finish unclaimed "$arr"
