#!/usr/bin/env bash
# The spec catalogs, read from the committed tree. Source after changeset.sh.
#
#   spec/<domain>/invariants/<id>.yaml   predicate · why · needs   (a10n format, see spec/README.md)
#
# An invariant's id is "<domain>/<id>": its domain folder and its file name.
#
# Markers (one token after the kind, per the engine's marker grammar):
#   // sr:invariant <domain>/<id>     code that upholds an invariant
#   // sr:proves <domain>/<id>        a test proving an invariant

# load_spec KIND — sets SPEC to a JSON array of {id, path, doc} for every
# spec/<domain>/KIND/*.yaml, id "<domain>/<name>". Unparseable YAML is refused, never skipped.
load_spec() {
  local kind="$1" f id out
  SPEC="[]"
  ls "$SR_TREE"/spec/*/"$kind"/*.yaml >/dev/null 2>&1 || return 0
  # sr-file field: the engine's own YAML reader (a case's PATH has the sloprail binaries, not yq).
  out="$(for f in "$SR_TREE"/spec/*/"$kind"/*.yaml; do
    id="${f#"$SR_TREE"/spec/}"; id="${id%%/*}/$(basename "$f" .yaml)"
    p="$(sr-file field "$f" predicate 2>&1)" || { printf 'ERR\t%s\t%s\n' "${f#"$SR_TREE"/}" "$p"; continue; }
    jq -n -c --arg id "$id" --arg path "${f#"$SR_TREE"/}" --arg p "$p" '{id: $id, path: $path, doc: {predicate: $p}}'
  done)"
  bad="$(printf '%s\n' "$out" | grep '^ERR' || true)"
  [ -z "$bad" ] || refuse "a file under spec/*/$kind is not valid YAML: $bad"
  SPEC="$(printf '%s\n' "$out" | jq -sc .)"
}

# is_test PATH — a test: a Go test file, anything under tests/, or an sr-test case of a rule.
is_test() { case "$1" in *_test.go | tests/* | .sloprail/*/tests/* | */.sloprail/*/tests/*) return 0 ;; esac; return 1; }

