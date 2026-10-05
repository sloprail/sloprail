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

# every changed file, tagged with the rule it belongs to
pairs=""
while IFS= read -r p; do
  [ -n "$p" ] || continue
  id="$(path_subject "$p")" || continue
  pairs="$pairs$id	$p
"
done < <(printf '%s' "$payload" | jq -r '.changeset.files[].path')

out='[]'
while IFS= read -r id; do
  [ -n "$id" ] || continue
  rule_split "$id" || continue
  cases="$(rule_cases "$id")"
  files="$(printf '%s' "$pairs" | awk -F'\t' -v id="$id" '$1 == id { print $2 }' | jq -R . | jq -sc .)"
  fp="$({
    while IFS= read -r c; do
      [ -n "$c" ] && find "$SR_TREE/$c" -type f
    done <<<"$cases"
    owner_files "$CASE_ROOT" "$CASE_NATURE" "$CASE_RULE"
    # the plugin name is part of the event name the verdict requires: <plugin>/<rule>
    plugin_manifest "$CASE_ROOT"
  } | files_sha)"
  out="$(printf '%s' "$out" | jq -c --arg c "$id" --argjson f "$files" --arg fp "$fp" '. + [{id: $c, files: $f, fingerprint: $fp}]')"
done < <(printf '%s' "$pairs" | cut -f1 | LC_ALL=C sort -u)
printf '%s\n' "$out"
