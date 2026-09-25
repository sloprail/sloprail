#!/usr/bin/env bash
# Reusable deterministic functions a gates/*.sh gate may source. See
# README.md in this directory for why these are plain functions and not a
# named-check dispatch layer.
#
# Each function: 0 = the condition holds, 1 = it does not (with a one-line
# reason on stdout). PROJECT_ROOT, if set, overrides $SR_WORKSPACE — set by a
# caller (or a test) that wants to point this at a tree other than the
# ambient one.
#
# Nothing here exits the calling script — same discipline as
# task-evidence-resolves/cite-links.sh: a pure library, sourced, not run.

_checks_root() { printf '%s' "${PROJECT_ROOT:-${SR_WORKSPACE:-.}}"; }

# file_exists <repo-relative-path>
file_exists() {
  local rel="${1:-}"
  local root
  root="$(_checks_root)"
  if [ -z "$rel" ]; then
    echo "file_exists: no path given"
    return 1
  fi
  case "$rel" in
    /*)
      echo "file_exists: '$rel' is an absolute path — pass a repo-relative one"
      return 1
      ;;
  esac
  if [ -f "$root/$rel" ]; then
    return 0
  fi
  echo "file_exists: no file at $rel"
  return 1
}

# task_is_done <group/task-name>
task_is_done() {
  local id="${1:-}"
  local root
  root="$(_checks_root)"
  if [ -z "$id" ]; then
    echo "task_is_done: no task id given"
    return 1
  fi
  case "$id" in
    */*) : ;;
    *)
      echo "task_is_done: '$id' is not a <group>/<task-name> id"
      return 1
      ;;
  esac
  if [ -d "$root/memories/tasks/$id" ]; then
    echo "task_is_done: memories/tasks/$id still exists — not done yet"
    return 1
  fi
  return 0
}

# unit_has_status <topic>/<NN_name> <expected-status>
unit_has_status() {
  local unit_id="${1:-}" expected="${2:-}"
  local root
  root="$(_checks_root)"
  if [ -z "$unit_id" ] || [ -z "$expected" ]; then
    echo "unit_has_status: needs <topic>/<NN_name> and an expected status"
    return 1
  fi
  local path="$root/memories/topics/$unit_id/UNIT.md"
  if [ ! -f "$path" ]; then
    echo "unit_has_status: no unit at memories/topics/$unit_id/UNIT.md"
    return 1
  fi
  command -v sr-file >/dev/null 2>&1 || {
    echo "unit_has_status: sr-file not on PATH"
    return 1
  }
  local status
  status="$(awk '
    NR == 1 && $0 == "---" { infm = 1; next }
    infm && $0 == "---" { exit }
    infm && $0 ~ /^status:[[:space:]]*/ {
      sub(/^status:[[:space:]]*/, "");
      gsub(/^["'"'"']|["'"'"']$/, "");
      print;
      exit
    }
  ' "$path")"
  if [ -z "$status" ]; then
    echo "unit_has_status: $path has no status: field in its frontmatter"
    return 1
  fi
  if [ "$status" = "$expected" ]; then
    return 0
  fi
  echo "unit_has_status: $path has status '$status', not '$expected'"
  return 1
}

# gh_repo_visibility_is <owner/repo> <public|private>
gh_repo_visibility_is() {
  local repo="${1:-}" expected="${2:-}"
  if [ -z "$repo" ] || [ -z "$expected" ]; then
    echo "gh_repo_visibility_is: needs <owner/repo> and an expected visibility"
    return 1
  fi
  case "$expected" in
    public|private) : ;;
    *)
      echo "gh_repo_visibility_is: expected visibility must be 'public' or 'private', got '$expected'"
      return 1
      ;;
  esac
  command -v gh >/dev/null 2>&1 || {
    echo "gh_repo_visibility_is: gh is not on PATH"
    return 1
  }
  local visibility
  visibility="$(gh repo view "$repo" --json visibility -q .visibility 2>&1)"
  if [ $? -ne 0 ]; then
    echo "gh_repo_visibility_is: gh repo view $repo failed: $visibility"
    return 1
  fi
  local visibility_lower
  visibility_lower="$(printf '%s' "$visibility" | tr '[:upper:]' '[:lower:]')"
  if [ "$visibility_lower" = "$expected" ]; then
    return 0
  fi
  echo "gh_repo_visibility_is: $repo is '$visibility_lower', not '$expected'"
  return 1
}
