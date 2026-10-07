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

# The files this call decides: `.subject.files` (one file for the requirement, every
# selected file for a check), as indexes into .changeset.files. The rest of the
# changeset is context this script does not judge. A subject that is not a list of
# paths is undecidable: apply.
idxs="$(printf '%s' "$payload" | jq -r '
  (.subject.files | if type == "array" then . else error("no subject") end) as $subj
  | [.changeset.files | to_entries[] | select(.value.path as $p | any($subj[]; . == $p)) | .key] | .[]' 2>/dev/null)" || exit 0
# A subject that matches no file of the changeset decided nothing: apply, never waive.
[ -n "$idxs" ] || exit 0

# One jq read per field, and any failure is undecidable: apply.
fld() { printf '%s' "$payload" | jq -r --argjson i "$1" ".changeset.files[\$i]$2" 2>/dev/null; }
# A field the payload does not carry is undecidable, never "empty": content or markers
# absent (a file too large, binary, or unreadable) must APPLY the requirement, so these
# read a field as a string / array or fail, and a failure exits 0 above.
req() { printf '%s' "$payload" | jq -r --argjson i "$1" ".changeset.files[\$i].$2 | if type == \"string\" then . else error(\"missing $2\") end" 2>/dev/null; }
mk() { printf '%s' "$payload" | jq -r --argjson i "$1" "[(.changeset.files[\$i].$2 | if type == \"array\" then . else error(\"missing $2\") end)[] | select(.kind == \"invariant\") | .fqn] | join(\"\\n\")" 2>/dev/null; }

# A committed change has a known result, and "before" is the range's base: a created
# file had nothing there, and a rename is the old path deleted and the new one created.
new_known=1 emptied="" applied=""
for i in $idxs; do
  status="$(fld "$i" '.status')" || exit 0
  path="$(fld "$i" '.path')" || exit 0
  oldpath="$(fld "$i" '.oldPath // ""')" || exit 0
  # Only what the status uses is read, and each of it is required: a missing field applies.
  oldc="" newc="" old_f="" new_f=""
  case "$status" in
    A) newc="$(req "$i" newContent)" || exit 0; new_f="$(mk "$i" newMarkers)" || exit 0 ;;
    M | R) oldc="$(req "$i" oldContent)" || exit 0; newc="$(req "$i" newContent)" || exit 0
       old_f="$(mk "$i" oldMarkers)" || exit 0; new_f="$(mk "$i" newMarkers)" || exit 0 ;;
    D) oldc="$(req "$i" oldContent)" || exit 0; old_f="$(mk "$i" oldMarkers)" || exit 0 ;;
  esac
  [ -n "$path" ] || exit 0

  # Each file is judged on its own, so a refusal names every file the change moves
  # (`rm a.go b.go`, both holding one pin), not only the first: apply() exits, so
  # it runs in a subshell, and the sentinel says whether the file was decided.
  case "$status" in
    A) res="$(had_old=0; lib_evaluate create "$path" "" "$newc" "" "$new_f"; printf '\037')" ;;
    M) res="$(had_old=1; lib_evaluate update "$path" "$oldc" "$newc" "$old_f" "$new_f"; printf '\037')" ;;
    D) res="$(had_old=1; lib_evaluate delete "$path" "$oldc" "" "$old_f" ""; printf '\037')" ;;
    R) res="$(had_old=1; lib_evaluate delete "${oldpath:-$path}" "$oldc" "" "$old_f" ""; had_old=0; lib_evaluate create "$path" "" "$newc" "" "$new_f"; printf '\037')" ;;
    *) exit 0 ;;
  esac
  case "$res" in
    "$(printf '\037')") ;; # decided: this file changes nothing pinned
    *"$(printf '\037')") applied="$applied$(printf '%s' "${res%"$(printf '\037')"}")"$'\n' ;;
    "") exit 0 ;;         # exited without deciding: apply, with no hint
    *) applied="$applied$res"$'\n' ;;
  esac
done

if [ -n "$applied" ]; then
  printf '%s' "$applied" | jq -s '{hint: (map(.hint) | join("\n\n")), what: (map(.what) | join(" "))}'
  exit 0
fi

lib_finish
