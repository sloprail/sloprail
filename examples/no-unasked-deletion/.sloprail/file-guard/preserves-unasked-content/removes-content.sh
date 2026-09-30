#!/usr/bin/env bash
# `when` for the user citation a removal needs — the file-guard's entry, over the
# committed Changeset (the gate's entry reads the Pre* events before the write): does
# this changeset remove content?
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
n="$(printf '%s' "$input" | jq -r '.changeset.files | length')" || exit 0
case "$n" in '' | *[!0-9]*) exit 0 ;; esac

total=0
i=0
while [ "$i" -lt "$n" ]; do
  idx="$i"
  i=$((i + 1))
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
