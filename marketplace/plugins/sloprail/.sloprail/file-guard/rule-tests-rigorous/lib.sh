#!/usr/bin/env bash
# Shared by subjects.sh, check-case.sh and prepare.sh: where a case lives and which rule owns it.
# Sourced, never run. Everything reads SR_TREE (the committed head), never the working tree.
#
# A case lives in its OWNING rule's folder: the owner is the folder, never inferred from the case's code.
#   <root>/.sloprail/<gate|file-guard|context>/<rule>/tests/<case>/
#   <root>/.sloprail/file-guard/structure.tests/<case>/        (the structure gate: one file, no folder)

# case_split <case dir, tree-relative> -> sets CASE_ROOT (the tree-relative dir that holds the `.sloprail`,
# "" for the repo root), CASE_NATURE (gate | file-guard | context | structure), CASE_RULE (the rule's folder
# name; "structure" for the structure gate) and CASE_NAME. Returns 1 for a path that is no case folder.
case_split() {
  local d="$1" rest a b c e
  CASE_ROOT="" CASE_NATURE="" CASE_RULE="" CASE_NAME=""
  case "$d" in
    .sloprail/*) rest="${d#.sloprail/}" ;;
    */.sloprail/*)
      CASE_ROOT="${d%%/.sloprail/*}"
      rest="${d#*/.sloprail/}"
      ;;
    *) return 1 ;;
  esac
  IFS=/ read -r a b c e <<<"$rest"
  if [ "$a" = file-guard ] && [ "$b" = structure.tests ] && [ -n "$c" ] && [ -z "$e" ]; then
    CASE_NATURE=structure CASE_RULE=structure CASE_NAME="$c"
    return 0
  fi
  case "$a" in gate | file-guard | context) ;; *) return 1 ;; esac
  [ -n "$b" ] && [ "$c" = tests ] && [ -n "$e" ] || return 1
  CASE_NATURE="$a" CASE_RULE="$b" CASE_NAME="$e"
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

# plugin_of <tree-relative root> -> the name of the plugin the `.sloprail` belongs to: the "name" of its
# plugin_manifest; empty for a rule that lives in the project itself.
plugin_of() {
  local m
  m="$(plugin_manifest "$1")"
  [ -n "$m" ] || return 0
  jq -r '.name // empty' "$m" 2>/dev/null || true
}

# owner_rule <tree-relative root> <rule> -> the name the owner's events carry in `.rule`: "<plugin>/<rule>"
# for a plugin's rule, the bare "<rule>" for one in the project ("structure" is the rule of the structure gate)
owner_rule() {
  local p
  p="$(plugin_of "$1")"
  if [ -n "$p" ]; then printf '%s/%s' "$p" "$2"; else printf '%s' "$2"; fi
}

# owner_kind <nature> -> the event kind the owner's decisions are recorded with
owner_kind() {
  case "$1" in
    gate) echo GateChecked ;;
    file-guard) echo FileGuardChecked ;;
    context) echo ContextActivated ;;
    structure) echo StructureChecked ;;
  esac
}

# owner_files <tree-relative root> <nature> <rule> -> the absolute path of every file that makes up the
# owning rule (its declaration, README, scripts, templates), one per line, sorted. Its cases (tests/) are
# not part of the rule: a sibling case is another subject.
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

rule_tests_rigorous_lib_loaded=1
