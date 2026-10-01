#!/usr/bin/env bash
# `when` for the user citation a removal needs — the file-guard's entry, over the
# committed Changeset (the gate's entry reads the Pre* events before the write): does
# this subject — `.subject.files`, one file for a requirement, the whole changeset
# only as context — remove content?
# Exit 0 — it does (a line present at the range's base is gone at head, or a file is
# deleted), so the commits must cite the user's words asking for it. Exit 1 — it only
# adds, so no ask is needed.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 does not permit anything — it
# APPLIES the requirement. So every path this script cannot decide exits 0, the
# fail-closed direction; only exit 1 waives the citation, and only on a decided
# pure addition.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset removes_content_lib_loaded
. "$lib_dir/removes-content-lib.sh" || exit 2
[ "${removes_content_lib_loaded:-}" = 1 ] || exit 2
lib_setup

input="$(cat)"
[ "$(printf '%s' "$input" | jq -r '.event.kind // empty')" = "Changeset" ] || exit 0
# The subject's files, as indexes into .changeset.files. A subject that is not a list
# of paths is undecidable: apply.
idxs="$(printf '%s' "$input" | jq -r '
  (.subject.files | if type == "array" then . else error("no subject") end) as $subj
  | [.changeset.files | to_entries[] | select(.value.path as $p | any($subj[]; . == $p)) | .key] | .[]' 2>/dev/null)" || exit 0
# A subject that matches no file of the changeset decided nothing: apply, never waive.
[ -n "$idxs" ] || exit 0

total=0
for idx in $idxs; do
  status="$(printf '%s' "$input" | jq -r --argjson i "$idx" '.changeset.files[$i].status')" || exit 0
  # A deletion is the largest removal there is — it always applies. (An added file
  # has nothing before it, so it removes nothing.)
  [ "$status" = "A" ] && continue
  # A status that carries content and has none (the field absent or not a string)
  # is undecidable, not empty: apply.
  old="$(printf '%s' "$input" | jq -r --argjson i "$idx" '.changeset.files[$i].oldContent | if type == "string" then . else error("missing oldContent") end' 2>/dev/null)" || exit 0
  if [ "$status" = "D" ]; then
    new=""
  else
    new="$(printf '%s' "$input" | jq -r --argjson i "$idx" '.changeset.files[$i].newContent | if type == "string" then . else error("missing newContent") end' 2>/dev/null)" || exit 0
  fi
  lib_count
  # A deletion applies whatever it held (even an empty file is a file lost).
  [ "$status" = "D" ] && lib_apply "${removed:-0}"
  total=$((total + ${removed:-0}))
done
[ "$total" -eq 0 ] && exit 1
lib_apply "$total"
