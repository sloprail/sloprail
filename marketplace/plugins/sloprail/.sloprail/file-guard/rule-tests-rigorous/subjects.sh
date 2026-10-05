#!/usr/bin/env bash
# One subject per RULE touched by the change: a changed case of the rule, or a changed file of the rule itself.
# id is the rule's folder (`<root>/.sloprail/<nature>/<rule>`; the structure gate's is its cases' folder,
# `<root>/.sloprail/file-guard/structure.tests`), files are the changed files that belong to it. The
# fingerprint is everything the verdict depends on: the rule's own files, ALL of its case folders (changed or
# not) and the plugin's manifest (its name is in the event name the verdict requires), read from SR_TREE.
# Editing any case, or the rule, judges that rule once.
# A rule with no case folder standing is still a subject (the engine refuses a selected file no subject names),
# with nothing to judge: the floor and the judge pass it. A case the range deleted is not among the changed
# files: the rule's `deletions` default is skip. Its remaining cases are still judged when some file of the
# rule changed.
set -uo pipefail
lib_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
unset rule_tests_rigorous_lib_loaded
. "$lib_dir/lib.sh" || exit 2
[ "${rule_tests_rigorous_lib_loaded:-}" = 1 ] || exit 2

payload="$(cat)"
if [ -z "${SR_TREE:-}" ]; then
  echo "rule-tests-rigorous subjects: SR_TREE is unset, so the rule's cases cannot be read. Refusing: a rule that could not be checked has not permitted." >&2
  exit 1
fi
printf '%s' "$payload" | jq -e '.changeset.files | type == "array"' >/dev/null || {
  echo "rule-tests-rigorous subjects: the changeset could not be read." >&2
  exit 1
}

# every changed file, tagged with the rule it belongs to. Paths are NUL-delimited and kept in arrays, so a
# path with a tab, a newline or a backslash reaches the subject whole.
ids=()
paths=()
while IFS= read -r -d '' p; do
  id="$(path_subject "$p")" || continue
  ids+=("$id")
  paths+=("$p")
done < <(printf '%s' "$payload" | jq -j '.changeset.files[].path + "\u0000"')

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
    [ "$other" = "$id" ] && sel+=("${paths[$i]}")
    i=$((i + 1))
  done
  files="$(jq -nc '$ARGS.positional' --args "${sel[@]}")"
  fp="$({
    while IFS= read -r c; do
      [ -n "$c" ] && find "$SR_TREE/$c" -type f
    done <<<"$cases"
    owner_files "$CASE_ROOT" "$CASE_NATURE" "$CASE_RULE"
    # the plugin name is part of the event name the verdict requires: <plugin>/<rule>
    plugin_manifest "$CASE_ROOT"
  } | files_sha)"
  out="$(printf '%s' "$out" | jq -c --arg c "$id" --argjson f "$files" --arg fp "$fp" '. + [{id: $c, files: $f, fingerprint: $fp}]')"
done
printf '%s\n' "$out"
