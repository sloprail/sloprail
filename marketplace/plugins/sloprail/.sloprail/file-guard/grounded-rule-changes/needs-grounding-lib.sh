#!/usr/bin/env bash
# Shared by needs-grounding.sh (the `when`), own-refusal-is-no-ground.sh and
# prepare.sh: which files of a changeset change what already stood in `.sloprail/`,
# so must be grounded. Sourced, never run.
#
# A file needs grounding when it is `.sloprail/config.yaml` (even a new one can carry
# `disabled:`), or when it already stood when the session began: the user's rules, or a
# rule from an earlier session. A file the agent ADDED in this session is its own
# work-in-progress: adding a rule weakens nothing, and fixing one it wrote earlier in
# the session (even after it passed a Stop) needs no misfire to cite. A rename is judged
# by the path it came from, since moving a rule out of `.sloprail/` removes it.
#
# "Stood when the session began" is `$SR_SESSION_START:<path>` in the range's snapshot
# `$SR_TREE`. Without it (no session start recorded, no snapshot) the range itself is
# the only evidence and only an ADDED file is new: strict in the safe direction.

# needing_paths PAYLOAD: print the paths needing grounding, one per line. Nonzero when
# the changeset cannot be read, so the caller fails closed.
needing_paths() {
  local rows status path old ref known=""
  rows="$(printf '%s' "$1" | jq -r '
    .changeset.files
    | if type == "array" then . else error("no files") end
    | .[] | [.status, .path, (.oldPath // "")] | @tsv' 2>/dev/null)" || return 1
  if [ -n "${SR_SESSION_START:-}" ] && [ -n "${SR_TREE:-}" ] &&
    git -C "$SR_TREE" cat-file -e "${SR_SESSION_START}^{commit}" 2>/dev/null; then
    known=1
  fi
  while IFS=$'\t' read -r status path old; do
    [ -n "$path" ] || continue
    if [ "$path" = ".sloprail/config.yaml" ] || [ "$old" = ".sloprail/config.yaml" ]; then
      printf '%s\n' "$path"
      continue
    fi
    ref="$path"
    [ "$status" = "R" ] && [ -n "$old" ] && ref="$old"
    if [ -n "$known" ]; then
      git -C "$SR_TREE" cat-file -e "${SR_SESSION_START}:${ref}" 2>/dev/null && printf '%s\n' "$path"
    else
      [ "$status" != "A" ] && printf '%s\n' "$path"
    fi
  done <<SR_EOF
$rows
SR_EOF
  return 0
}
needs_grounding_lib_loaded=1
