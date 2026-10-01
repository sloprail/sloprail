#!/usr/bin/env bash
# `when` for the user citation a scanner's keywords need: does this write DROP a
# keyword the scanner already declared? Exit 0 — it does, so the write must cite
# the user's words asking for it. Exit 1 — it does not (a new scanner, or one that
# only adds keywords), so no citation is required. Deleting the scanner drops
# every keyword it declared, so a delete of one that declared any applies too.
# A created scanner can drop keywords too: one declared this session and committed
# as a new file drops whatever the registry owes that the file no longer declares.
#
# This is the file-guard entry: it reads the Changeset, each scanner as it stood at
# the range's base against how it stands at head (a rename is the old scanner
# deleted and the new one created). The gate of the same name keeps the pre-write
# copy.
#
# THIS IS A `when` PREDICATE, NOT A CHECK: exit 0 does not permit anything — it
# APPLIES the requirement. So every path this script cannot decide exits 0, the
# fail-closed direction; only exit 1 waives the citation, and only on a decided
# "drops nothing".
#
# The failure it exists for, measured on a real Haiku run: refused by the
# coverage gate for a search that missed keywords, the agent rewrote the
# scanner's keywords to fit the search it had already run — the declaration
# weakened to match the work, instead of the work meeting the declaration.
lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset drops_keywords_lib_loaded
. "$lib_dir/drops-keywords-lib.sh" || exit 2
[ "${drops_keywords_lib_loaded:-}" = 1 ] || exit 2
lib_setup

payload="$(cat)"
[ "$(printf '%s' "$payload" | jq -r '.event.kind // ""' 2>/dev/null)" = "Changeset" ] || exit 0

# The subject's scanners — `.subject.files`, one file for a requirement — as the
# changes to judge (the rest of the changeset is context this script does not read;
# a check handed the whole changeset has every file in its subject), each {op, path, old, new}: a
# rename is the old scanner deleted and the new one created.
# A content field a status must carry and does not (absent or not a string) is
# undecidable, not empty: jq errors, and the requirement applies.
changes="$(printf '%s' "$payload" | jq -c '
  def need(k): if (.[k] | type) == "string" then .[k] else error("missing " + k) end;
  (.subject.files | if type == "array" then . else error("no subject") end) as $subj
  | [ .changeset.files[] | select(.path as $p | any($subj[]; . == $p))
    | if .status == "R" then
        ({op: "delete", path: .oldPath, old: need("oldContent"), new: ""}, {op: "create", path: .path, old: "", new: need("newContent")})
      elif .status == "D" then {op: "delete", path: .path, old: need("oldContent"), new: ""}
      elif .status == "A" then {op: "create", path: .path, old: "", new: need("newContent")}
      else {op: "update", path: .path, old: need("oldContent"), new: need("newContent")} end ]' 2>/dev/null)" || exit 0
n="$(printf '%s' "$changes" | jq 'length' 2>/dev/null)" || exit 0
case "$n" in '' | *[!0-9]*) exit 0 ;; esac

i=0
while [ "$i" -lt "$n" ]; do
  f() { printf '%s' "$changes" | jq -r --argjson i "$i" ".[\$i].$1" 2>/dev/null; }
  op="$(f op)" || exit 0
  path="$(f path)" || exit 0
  old="$(f old)" || exit 0
  new="$(f new)" || exit 0
  i=$((i + 1))
  lib_owed
  if [ "$op" = delete ]; then
    lib_check_delete
  else
    lib_check
  fi
done
lib_waive
