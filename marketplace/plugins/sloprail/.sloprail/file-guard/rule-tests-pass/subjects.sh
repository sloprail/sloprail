#!/usr/bin/env bash
# One subject per RULE touched by the change (a changed case of the rule, or a changed file of the rule itself),
# as rule-tests-rigorous does: each is cached on its own and runs only that rule's cases, so a repo with many
# rules is not one long run that times out under load. id is the rule's folder
# (`<root>/.sloprail/<nature>/<rule>`; the structure gate's is its cases' folder,
# `<root>/.sloprail/file-guard/structure.tests`). files are the changed paths of the rule whose CONTENT changed
# (a mode-only change, same bytes, is still a subject, the engine needs one for the selected file, but lists
# no file). The fingerprint is what the verdict reads: the rule's own files, ALL its case folders and the
# plugin's manifest, read from SR_TREE.
set -uo pipefail
lib_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
unset rule_tests_pass_lib_loaded
. "$lib_dir/lib.sh" || exit 2
[ "${rule_tests_pass_lib_loaded:-}" = 1 ] || exit 2

payload="$(cat)"
if [ -z "${SR_TREE:-}" ]; then
  echo "rule-tests-pass subjects: SR_TREE is unset, so the rules and tests cannot be read. Refusing: a rule that could not be checked has not permitted." >&2
  exit 1
fi
printf '%s' "$payload" | jq -e '.changeset.files | type == "array"' >/dev/null || {
  echo "rule-tests-pass subjects: the changeset could not be read." >&2
  exit 1
}

# every changed file, tagged with the rule it belongs to and whether its content changed. Paths are
# NUL-delimited and kept in arrays, so a path with a tab, a newline or a backslash reaches the subject whole.
ids=()
paths=()
real=()
while IFS= read -r -d '' flag && IFS= read -r -d '' p; do
  id="$(path_subject "$p")" || continue
  ids+=("$id")
  paths+=("$p")
  real+=("$flag")
done < <(printf '%s' "$payload" | jq -j '.changeset.files[] | (if (.status != "M" or .oldContent != .newContent) then "1" else "0" end) + "\u0000" + .path + "\u0000"')

uniq=()
for id in ${ids[@]+"${ids[@]}"}; do
  seen=0
  for u in ${uniq[@]+"${uniq[@]}"}; do
    [ "$u" = "$id" ] && seen=1
  done
  [ "$seen" = 1 ] || uniq+=("$id")
done

out='[]'
for id in ${uniq[@]+"${uniq[@]}"}; do
  rule_split "$id" || continue
  cases="$(rule_cases "$id")"
  sel=()
  i=0
  for other in "${ids[@]}"; do
    [ "$other" = "$id" ] && [ "${real[$i]}" = 1 ] && sel+=("${paths[$i]}")
    i=$((i + 1))
  done
  files="$(jq -nc '$ARGS.positional' --args ${sel[@]+"${sel[@]}"})"
  fp="$({
    while IFS= read -r c; do
      [ -n "$c" ] && find "$SR_TREE/$c" -type f
    done <<<"$cases"
    owner_files "$CASE_ROOT" "$CASE_NATURE" "$CASE_RULE"
    plugin_manifest "$CASE_ROOT"
  } | files_sha)"
  out="$(printf '%s' "$out" | jq -c --arg c "$id" --argjson f "$files" --arg fp "$fp" '. + [{id: $c, files: $f, fingerprint: $fp}]')"
done
printf '%s\n' "$out"
