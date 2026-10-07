#!/usr/bin/env bash
# The spec catalogs, read from the committed tree. Source after changeset.sh.
#
#   spec/invariants/<id>.yaml     statement                      (features: the user's words)
#   spec/capabilities/<id>.yaml   statement · providers.<harness>: {docs: [...], runs: [...]} | false
#
# The file name is the id. Harnesses are the top-level <harness>-mock/ dirs.
#
# Markers (one token after the kind, per the engine's marker grammar):
#   // sr:invariant <id>              code that upholds an invariant
#   // sr:proves <id>                 a test proving an invariant
#   // sr:capability <id>             a capability's one implementation, in internal/
#   // sr:provides <id>/<harness>     that harness's adapter for it
#   // sr:proves <id>/<harness>       a test proving it for that harness

# load_spec KIND — sets SPEC to a JSON array of {id, path, doc} for every
# spec/KIND/*.yaml. Unparseable YAML is refused, never skipped.
load_spec() {
  local kind="$1" out
  SPEC="[]"
  ls "$SR_TREE/spec/$kind"/*.yaml >/dev/null 2>&1 || return 0
  # one yq over every file: it names each document by its file
  out="$(yq -o=json -I=0 '{"id": (filename | split("/") | .[-1] | sub("\\.yaml$"; "")), "path": ("spec/'"$kind"'/" + (filename | split("/") | .[-1])), "doc": .}' \
    "$SR_TREE/spec/$kind"/*.yaml 2>&1)" || refuse "a file under spec/$kind is not valid YAML: $out"
  SPEC="$(jq -sc . <<<"$out")"
}

# harnesses — the harness mocks in the committed tree, one name per line.
harnesses() { (cd "$SR_TREE" && for d in *-mock; do [ -d "$d" ] && echo "${d%-mock}"; done); }

