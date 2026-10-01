#!/bin/sh
# Refuses a guardrail HOOK SCRIPT that carries a shape measured to make a rule
# silently inert. This is a file-guard's check: it receives a Changeset payload on
# stdin (every `.sh`/`.md.j2` hook the range touched, under `.changeset.files[]`)
# and its cwd is the guard's own folder, so rules/ and the RULE.md files resolve
# beside it.
#
# Only the shapes recorded as `enforced: true` in rules/<name>/RULE.md are
# checked here — each is one deterministic grep below. A rule that needs
# judgement is documented and left un-enforced (its RULE.md carries no grep): a
# check that fires on taste gets switched off, and then the decidable ones go
# with it.
#
# Exit 0 permits, non-zero refuses.

lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset check_rules_lib_loaded
. "$lib_dir/check-rules-lib.sh" || exit 2
[ "${check_rules_lib_loaded:-}" = 1 ] || exit 2
lib_setup

event="$(cat)"

# The payload is a Changeset: the files this guard's match selected are under
# `.changeset.files[]` as {path, status, newContent, ...}, committed content that is
# always known (there is no newContentKnown on a changeset). Every hook is scanned
# and every finding names its file.
kind="$(printf '%s' "$event" | jq -r '.event.kind // empty' 2>/dev/null)"
[ "$kind" = "Changeset" ] || {
  echo "authoring-slop: expected a Changeset event, got '${kind:-nothing}', so it could not be checked" >&2
  exit 1
}
files_n="$(printf '%s' "$event" | jq -r '.changeset.files | length')" || {
  echo "authoring-slop: could not read the changeset's files, so it could not be checked" >&2
  exit 1
}

findings=""
i=0
while [ "$i" -lt "$files_n" ]; do
  path="$(printf '%s' "$event" | jq -r --argjson i "$i" '.changeset.files[$i].path')" || {
    echo "authoring-slop: could not read file $i of the changeset, so it could not be checked" >&2
    exit 1
  }
  idx="$i"
  i=$((i + 1))
  # The grep is scoped to `.sh` hooks: its signatures (`newContent` present, a
  # tool-name alternation, a model-invocation flag) would false-positive on a
  # `.md.j2` template, which legitimately names fields and tools in prose. A
  # template is the judge's business, not this grep's.
  case "$path" in
    *.sh) ;;
    *) continue ;;
  esac
  body="$(printf '%s' "$event" | jq -r --argjson i "$idx" '.changeset.files[$i].newContent')" || {
    echo "authoring-slop: could not read $path from the changeset, so it could not be checked" >&2
    exit 1
  }
  lib_scan
done
lib_report
exit 0
