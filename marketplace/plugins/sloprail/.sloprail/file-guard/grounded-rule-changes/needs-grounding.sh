#!/usr/bin/env bash
# `when` for the citation a change to the project's rules needs: does THIS subject
# (`.subject.files`, one file for a requirement) change what already stood in
# `.sloprail/`? A file added in the range does not, except `.sloprail/config.yaml`.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 APPLIES the requirement, exit 1
# waives it. Every case this script cannot decide exits 0, the fail-closed way; only
# a decided pure addition of a non-config file waives.
#
# On apply it prints the rule's own advice as a hint: what to cite, and the way out
# that is never taken.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset needs_grounding_lib_loaded
. "$lib_dir/needs-grounding-lib.sh" || exit 2
[ "${needs_grounding_lib_loaded:-}" = 1 ] || exit 2

apply() {
  jq -n '{hint: "A change to what already stands in .sloprail/ (a rule edited, a rule deleted, config.yaml changed) must be grounded in one of two things. Either the user asked for it: cite their exact words. Or a bug actually happened: cite the tool output that shows the rule misfiring on correct work (a command result, not the rule refusing your own bad work). If neither is true, ask the user, and fix your work instead of the rule. Never disable, loosen or delete a rule to get past its refusal."}'
  exit 0
}

input="$(cat)"
[ "$(printf '%s' "$input" | jq -r '.event.kind // empty')" = "Changeset" ] || apply
# The range's paths that need grounding, narrowed to this subject's files.
needing="$(needing_paths "$input")" || apply
subject="$(printf '%s' "$input" | jq -r '.subject.files | if type == "array" then .[] else error("no subject") end' 2>/dev/null)" || apply
[ -n "$subject" ] || apply
while IFS= read -r p; do
  [ -n "$p" ] || continue
  if grep -qxF -- "$p" <<<"$needing"; then
    apply
  fi
done <<SR_EOF
$subject
SR_EOF
exit 1
