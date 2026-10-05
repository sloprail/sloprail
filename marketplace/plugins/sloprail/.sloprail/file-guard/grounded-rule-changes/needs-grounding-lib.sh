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

# A file whose new content is byte-identical to its content at an earlier commit on the
# default branch, where every commit on the default branch that changed it AFTER that
# version carried no citation trailer, only undoes uncited changes: it restores what the
# default branch had before an agent edited it without grounding. That needs no citation.
# Undoing a cited (user-approved) change still does, and so does any content the default
# branch never had. Any Sloprail-Cites-* trailer counts as cited (this is not a judge of
# whether it resolved), the safe direction. Anything undecidable is "not a revert".
#
# The default branch is origin/HEAD, else main, else master, in $SR_TREE's repository.
default_branch_ref() {
  local r
  for r in refs/remotes/origin/HEAD refs/heads/main refs/heads/master; do
    git -C "$SR_TREE" rev-parse -q --verify "$r^{commit}" >/dev/null 2>&1 && { printf '%s\n' "$r"; return 0; }
  done
  return 1
}

# is_landed_revert PATH: exit 0 when PATH at $SR_HEAD equals PATH at an earlier commit of
# the default branch and no commit after that version cited anything.
is_landed_revert() {
  local path="$1" want ref c have base inrange
  [ -n "${SR_TREE:-}" ] || return 1
  # The range's own commits are the change under judgment, never the version it restores.
  base="${SR_BASE:-${SR_SESSION_START:-}}"
  [ -n "$base" ] || return 1
  inrange="$(git -C "$SR_TREE" rev-list "${base}..${SR_HEAD:-HEAD}" 2>/dev/null)" || return 1
  want="$(git -C "$SR_TREE" rev-parse -q --verify "${SR_HEAD:-HEAD}:${path}" 2>/dev/null)" || return 1
  ref="$(default_branch_ref)" || return 1
  # Newest first; the commits newer than the matching version are the ones being undone.
  while read -r c; do
    [ -n "$c" ] || continue
    printf '%s\n' "$inrange" | grep -qxF -- "$c" && continue
    have="$(git -C "$SR_TREE" rev-parse -q --verify "${c}:${path}" 2>/dev/null)" || have=""
    [ "$have" = "$want" ] && return 0
    git -C "$SR_TREE" log -1 --format=%B "$c" 2>/dev/null | grep -qiE '^Sloprail-Cites-[A-Za-z]+:' && return 1
  done <<SR_REV_EOF
$(git -C "$SR_TREE" log --format=%H "$ref" -- "$path" 2>/dev/null)
SR_REV_EOF
  return 1
}

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
    case "/$path" in */.sloprail/config.yaml) is_config=1 ;; *) is_config="" ;; esac
    case "/$old" in */.sloprail/config.yaml) is_config=1 ;; esac
    if [ -n "$is_config" ]; then
      printf '%s\n' "$path"
      continue
    fi
    # Undoing only uncited changes (a revert to a version the default branch had) needs none.
    if [ "$status" != "A" ] && [ "$status" != "D" ] && [ "$status" != "R" ] && is_landed_revert "$path"; then
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
