#!/usr/bin/env bash
# Reads a Changeset event's files, for every file-guard script of this plugin that
# walks `changeset.files`, so they agree on what a missing field is. A pure library:
# sourced, never run; nothing here exits. Each function prints and returns jq's
# status, and the caller decides what a failure means (refuse, apply, ask the judge).
#
# A content field is a STRING the engine always sends. An absent one is undecidable,
# never the empty file it would read as: cs_text fails on it.

# cs_count EVENT  ->  how many files the changeset holds.
cs_count() { printf '%s' "$1" | jq -r '.changeset.files | length'; }

# cs_indexes EVENT  ->  one index per line: the files of `.subject.files` (the unit a
# `when` is asked about: one file for a requirement, every selected file for a check),
# as indexes into `.changeset.files`. The rest of the changeset is context. Fails when
# the subject is not a list of paths, which is undecidable.
cs_indexes() {
  printf '%s' "$1" | jq -r '
    (.subject.files | if type == "array" then . else error("no subject") end) as $subj
    | [.changeset.files | to_entries[] | select(.value.path as $p | any($subj[]; . == $p)) | .key] | .[]'
}

# cs_get EVENT I EXPR  ->  EXPR applied to file I (for example `.path`, `.status`).
cs_get() { printf '%s' "$1" | jq -r --argjson i "$2" ".changeset.files[\$i]$3"; }

# cs_text EVENT I FIELD  ->  the string FIELD (oldContent, newContent) of file I;
# fails when it is absent or not a string.
cs_text() {
  printf '%s' "$1" | jq -r --argjson i "$2" --arg f "$3" \
    '.changeset.files[$i][$f] | if type == "string" then . else error("missing " + $f) end'
}

# LOADED SENTINEL — keep this the LAST line. A caller unsets it, sources this file,
# and checks it: only the last line proves the whole file ran.
changeset_lib_loaded=1
