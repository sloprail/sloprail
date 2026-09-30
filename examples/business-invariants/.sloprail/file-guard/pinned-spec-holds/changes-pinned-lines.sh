#!/usr/bin/env bash
# `when` for the user citation a pinned rule needs — the FILE-GUARD entry: it reads the
# Changeset, each file's oldContent (what the range's base held) against its newContent
# (what head holds), and searches the markers in the committed head ($SR_TREE) and at
# the base ($SR_BASE), never the working tree. The gate's entry reads the pending
# write. What counts as a pinned change, and why, is changes-pinned-lines-lib.sh's.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 APPLIES the requirement, and every
# path this script cannot decide exits 0. Only a decided "changes nothing pinned" exits
# 1, with the `{"waived": …}` sentinel only-when-pinned.sh looks for.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset changes_pinned_lines_lib_loaded
. "$lib_dir/changes-pinned-lines-lib.sh" || exit 0
[ "${changes_pinned_lines_lib_loaded:-}" = 1 ] || exit 0
lib_setup

payload="$(cat)"
[ "$(printf '%s' "$payload" | jq -r '.event.kind // ""' 2>/dev/null)" = "Changeset" ] || exit 0

# The committed head and the range's base. Undecidable without them: apply.
[ -n "${SR_TREE:-}" ] && [ -n "${SR_BASE:-}" ] || exit 0
lib_tree "$SR_TREE" "$SR_BASE"

n="$(printf '%s' "$payload" | jq -r '.changeset.files | length' 2>/dev/null)" || exit 0
case "$n" in '' | *[!0-9]*) exit 0 ;; esac

# One jq read per field, and any failure is undecidable: apply.
fld() { printf '%s' "$payload" | jq -r --argjson i "$1" ".changeset.files[\$i]$2" 2>/dev/null; }
mk() { printf '%s' "$payload" | jq -r --argjson i "$1" "[(.changeset.files[\$i].$2 // [])[] | select(.kind == \"invariant\") | .fqn] | join(\"\\n\")" 2>/dev/null; }

# A committed change has a known result, and "before" is the range's base: a created
# file had nothing there, and a rename is the old path deleted and the new one created.
new_known=1 emptied=""
i=0
while [ "$i" -lt "$n" ]; do
  status="$(fld "$i" '.status')" || exit 0
  path="$(fld "$i" '.path')" || exit 0
  oldpath="$(fld "$i" '.oldPath // ""')" || exit 0
  oldc="$(fld "$i" '.oldContent // ""')" || exit 0
  newc="$(fld "$i" '.newContent // ""')" || exit 0
  old_f="$(mk "$i" oldMarkers)" || exit 0
  new_f="$(mk "$i" newMarkers)" || exit 0
  i=$((i + 1))
  [ -n "$path" ] || exit 0

  case "$status" in
    A) had_old=0; lib_evaluate create "$path" "" "$newc" "" "$new_f" ;;
    M) had_old=1; lib_evaluate update "$path" "$oldc" "$newc" "$old_f" "$new_f" ;;
    D) had_old=1; lib_evaluate delete "$path" "$oldc" "" "$old_f" "" ;;
    R)
      had_old=1; lib_evaluate delete "${oldpath:-$path}" "$oldc" "" "$old_f" ""
      had_old=0; lib_evaluate create "$path" "" "$newc" "" "$new_f"
      ;;
    *) exit 0 ;;
  esac
done

lib_finish
