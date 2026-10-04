#!/usr/bin/env bash
# Shared by subjects.sh and check.sh: which rule a path belongs to, and the two facts about the
# rule's history the rollout depends on. One library, two thin entries.

# rule_of PATH — prints `<nature>/<name>` when PATH is inside a rule's folder, nothing otherwise.
# `.sloprail/config.yaml` and `.sloprail/file-guard/structure.yaml` are not a rule's.
rule_of() {
  case "$1" in
    .sloprail/file-guard/*/* | .sloprail/gate/*/* | .sloprail/context/*/*)
      local rest="${1#.sloprail/}"
      local nature="${rest%%/*}"
      rest="${rest#*/}"
      printf '%s/%s\n' "$nature" "${rest%%/*}"
      ;;
  esac
}

# at_rev REV PATH — does PATH exist at REV in the project's repository (SR_WORKSPACE)? 0 yes,
# 1 no, 2 the repository could not be read (the caller refuses: a rule that could not be
# checked has not been approved). An empty REV (a range with no base) is "no": nothing stood
# there.
at_rev() {
  local rev="$1" p="$2"
  git -C "$SR_WORKSPACE" rev-parse --git-dir >/dev/null 2>&1 || return 2
  [ -n "$rev" ] || return 1
  git -C "$SR_WORKSPACE" cat-file -e "$rev:$p" 2>/dev/null && return 0
  return 1
}

rule_tests_lib_loaded=1
