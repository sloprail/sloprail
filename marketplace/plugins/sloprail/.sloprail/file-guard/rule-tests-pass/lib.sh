#!/usr/bin/env bash
# Shared by subjects.sh and check.sh: where a rule and its cases live, and which changed paths are content
# changes. Sourced, never run. Everything reads SR_TREE (the committed head), never the working tree.
#
# The subject is a RULE, and a rule owns its cases. A case lives in its OWNING rule's folder: the owner is the
# folder, never inferred from the case's code.
#   <root>/.sloprail/<gate|file-guard|context>/<rule>/tests/<case>/
#   <root>/.sloprail/file-guard/structure.tests/<case>/        (the structure gate: one file, no folder)
# A subject's id is the rule's folder, `<root>/.sloprail/<nature>/<rule>`; for the structure gate it is its
# cases' folder, `<root>/.sloprail/file-guard/structure.tests` (the rule itself is the one file beside it,
# `<root>/.sloprail/file-guard/structure.yaml`).

# path_subject <tree-relative path> -> prints the id of the subject (the owning rule) the path belongs to: a file
# of the rule (its declaration, README, scripts, templates) or a file under its tests/ (a case's, or a stray one
# that is no case). Returns 1, printing nothing, for a path that belongs to no rule.
path_subject() {
  local p="$1" root="" rest a b c
  case "$p" in
    .sloprail/*) rest="${p#.sloprail/}" ;;
    */.sloprail/*)
      root="${p%%/.sloprail/*}/"
      rest="${p#*/.sloprail/}"
      ;;
    *) return 1 ;;
  esac
  # split by parameter expansion, not `read`: read stops at a newline, and a path may hold one
  a="${rest%%/*}"
  b=""
  c=""
  case "$rest" in
    */*)
      b="${rest#*/}"
      case "$b" in
        */*)
          c="${b#*/}"
          b="${b%%/*}"
          ;;
      esac
      ;;
  esac
  if [ "$a" = file-guard ]; then
    if [ "$b" = structure.yaml ] && [ -z "$c" ]; then
      printf '%s.sloprail/file-guard/structure.tests\n' "$root"
      return 0
    fi
    if [ "$b" = structure.tests ]; then
      # any file under structure.tests/, a case's or a stray one that is no case, belongs to the structure gate
      [ -n "$c" ] || return 1
      printf '%s.sloprail/file-guard/structure.tests\n' "$root"
      return 0
    fi
  fi
  # config.yaml is project settings, not code a case runs: it belongs to no subject (the rule's match excludes it)
  [ "$rest" = config.yaml ] && return 1
  case "$a" in
    gate | file-guard | context)
      if [ -n "$b" ] && [ -n "$c" ]; then
        printf '%s.sloprail/%s/%s\n' "$root" "$a" "$b"
        return 0
      fi
      ;;
  esac
  # any other file of the `.sloprail/` (a shared lib/, schemas/, a stray file) is no rule's own: it belongs to
  # the root's marker, `<root>/.sloprail`, which subjects.sh fans out to every rule of the root
  printf '%s.sloprail\n' "$root"
}

# is_marker <subject id> -> 0 for a root-wide marker (`.sloprail`, `<root>/.sloprail`), 1 for a rule's id
is_marker() {
  case "$1" in .sloprail | */.sloprail) return 0 ;; esac
  return 1
}

# marker_root <marker> -> the tree-relative dir that holds the `.sloprail` ("" for the repo root)
marker_root() {
  local m="${1%.sloprail}"
  printf '%s' "${m%/}"
}

# root_rules <tree-relative root> -> the id of every rule of that root's `.sloprail`, one per line, sorted: its
# gate/file-guard/context folders and the structure gate (when structure.yaml or structure.tests exists)
root_rules() {
  local root="$1" pre="" base n d
  [ -n "$root" ] && pre="$root/"
  base="$(root_abs "$root")/.sloprail"
  for n in gate file-guard context; do
    [ -d "$base/$n" ] || continue
    find "$base/$n" -mindepth 1 -maxdepth 1 -type d | LC_ALL=C sort | while IFS= read -r d; do
      [ "$n" = file-guard ] && [ "${d##*/}" = structure.tests ] && continue
      printf '%s.sloprail/%s/%s\n' "$pre" "$n" "${d##*/}"
    done
  done
  if [ -f "$base/file-guard/structure.yaml" ] || [ -d "$base/file-guard/structure.tests" ]; then
    printf '%s.sloprail/file-guard/structure.tests\n' "$pre"
  fi
}

# shared_files <tree-relative root> -> the absolute path of every shared file of that root's `.sloprail` (those
# that belong to no rule: lib/, schemas/, ...), one per line, sorted
shared_files() {
  local root="$1" pre="" base f
  [ -n "$root" ] && pre="$root/"
  base="$(root_abs "$root")/.sloprail"
  [ -d "$base" ] || return 0
  find "$base" -type f | LC_ALL=C sort | while IFS= read -r f; do
    [ "$(path_subject "${f#"$SR_TREE"/}" 2>/dev/null)" = "${pre}.sloprail" ] && printf '%s\n' "$f"
  done
}

# rule_users <rule id> <file name> -> the ids of the OTHER rules of its root whose own files name `<rule>/<file name>`:
# a file a rule keeps for another to source (`../<rule>/cite-links.sh`). A change to it can break their cases.
rule_users() {
  local id="$1" fname="$2" root name o f
  rule_split "$id" || return 0
  [ "$CASE_NATURE" = structure ] && return 0
  root="$CASE_ROOT" name="$CASE_RULE"
  root_rules "$root" | while IFS= read -r o; do
    [ "$o" = "$id" ] && continue
    rule_split "$o" || continue
    [ "$CASE_NATURE" = structure ] && continue
    owner_files "$CASE_ROOT" "$CASE_NATURE" "$CASE_RULE" | while IFS= read -r f; do
      if grep -qF -- "$name/$fname" "$f" 2>/dev/null; then
        printf '%s\n' "$o"
        break
      fi
    done
  done
}

# rule_refs <rule id> -> the absolute path of every file of another rule that this rule's own files name as
# `<other rule>/<file>` (what rule_users looks for from the other side), one per line
rule_refs() {
  local id="$1" root o f own
  rule_split "$id" || return 0
  [ "$CASE_NATURE" = structure ] && return 0
  root="$CASE_ROOT"
  own="$(owner_files "$CASE_ROOT" "$CASE_NATURE" "$CASE_RULE")"
  [ -n "$own" ] || return 0
  root_rules "$root" | while IFS= read -r o; do
    [ "$o" = "$id" ] && continue
    rule_split "$o" || continue
    [ "$CASE_NATURE" = structure ] && continue
    local name="$CASE_RULE" nat="$CASE_NATURE" rr="$CASE_ROOT" g
    owner_files "$rr" "$nat" "$name" | while IFS= read -r f; do
      while IFS= read -r g; do
        if grep -qF -- "$name/${f##*/}" "$g" 2>/dev/null; then
          printf '%s\n' "$f"
          break
        fi
      done <<<"$own"
    done
  done
}

# rule_split <subject id> -> sets CASE_ROOT (the tree-relative dir that holds the `.sloprail`, "" for the repo
# root), CASE_NATURE (gate | file-guard | context | structure) and CASE_RULE (the rule's folder name;
# "structure" for the structure gate). Returns 1 for an id that is no rule.
rule_split() {
  local d="$1" rest a b
  CASE_ROOT="" CASE_NATURE="" CASE_RULE=""
  case "$d" in
    .sloprail/*) rest="${d#.sloprail/}" ;;
    */.sloprail/*)
      CASE_ROOT="${d%%/.sloprail/*}"
      rest="${d#*/.sloprail/}"
      ;;
    *) return 1 ;;
  esac
  a="${rest%%/*}"
  b=""
  case "$rest" in
    */*)
      b="${rest#*/}"
      b="${b%%/*}"
      ;;
  esac
  if [ "$a" = file-guard ] && [ "$b" = structure.tests ]; then
    CASE_NATURE=structure CASE_RULE=structure
    return 0
  fi
  case "$a" in gate | file-guard | context) ;; *) return 1 ;; esac
  [ -n "$b" ] || return 1
  CASE_NATURE="$a" CASE_RULE="$b"
}

# rule_cases <subject id> -> the tree-relative folder of every case of the rule, one per line, sorted
rule_cases() {
  local id="$1" d c
  rule_split "$id" || return 0
  if [ "$CASE_NATURE" = structure ]; then d="$id"; else d="$id/tests"; fi
  [ -d "$SR_TREE/$d" ] || return 0
  find "$SR_TREE/$d" -mindepth 1 -maxdepth 1 -type d | LC_ALL=C sort | while IFS= read -r c; do
    printf '%s\n' "${c#"$SR_TREE"/}"
  done
}

# root_abs <tree-relative root> -> the absolute dir that holds the `.sloprail`
root_abs() {
  if [ -n "$1" ]; then printf '%s/%s' "$SR_TREE" "$1"; else printf '%s' "$SR_TREE"; fi
}

# plugin_manifest <tree-relative root> -> the absolute path of the nearest `.claude-plugin/plugin.json` at or
# above the dir that holds the `.sloprail` (up to the tree's root); nothing for a rule that lives in the project.
plugin_manifest() {
  local d
  d="$(root_abs "$1")"
  while :; do
    if [ -f "$d/.claude-plugin/plugin.json" ]; then
      printf '%s\n' "$d/.claude-plugin/plugin.json"
      return 0
    fi
    [ "$d" = "$SR_TREE" ] && return 0
    case "$d" in "$SR_TREE"/*) ;; *) return 0 ;; esac
    d="$(dirname "$d")"
  done
}

# owner_files <tree-relative root> <nature> <rule> -> the absolute path of every file that makes up the
# rule itself (its declaration, README, scripts, templates), one per line, sorted. Its cases (tests/) are
# not part of this list: they are the rule's cases, listed by rule_cases.
owner_files() {
  local base
  base="$(root_abs "$1")/.sloprail"
  if [ "$2" = structure ]; then
    [ -f "$base/file-guard/structure.yaml" ] && printf '%s\n' "$base/file-guard/structure.yaml"
    return 0
  fi
  [ -d "$base/$2/$3" ] || return 0
  find "$base/$2/$3" -type f -not -path "$base/$2/$3/tests/*" | LC_ALL=C sort
}

# files_sha -> one sha256 over the tree-relative names and the contents of the files whose absolute paths
# are on stdin, sorted
files_sha() {
  local f
  LC_ALL=C sort | while IFS= read -r f; do
    [ -f "$f" ] || continue
    printf '%s\n' "${f#"$SR_TREE"/}"
    cat "$f"
  done | { shasum -a 256 2>/dev/null || sha256sum; } | cut -d' ' -f1
}

rule_tests_pass_lib_loaded=1
