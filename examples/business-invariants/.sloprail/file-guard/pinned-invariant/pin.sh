# Sourced by pin-still-matches-head.sh and pinned-text.sh, so both read a pin the
# same way. An sr:invariant marker's fqn is a pinned spec reference:
#   <repo>@<sha>:<path>#L<start>-<end>
# The fqn is written by the agent being judged, so nothing in it reaches git
# unchecked: the sha must be a hex object name (an fqn whose "sha" is
# `--output=<file>` would otherwise have git write that file), and the range must
# be a real one inside the file (a range past its end pins no text, and a judge
# handed an empty <pinned> rules against nothing). The sha is resolved to the
# commit it names and must be a prefix of it: a short sha that a branch or tag of
# the same name shadows would otherwise read that ref's file.
#
# A pin must point into a spec: `SPEC.md` at any depth, or a `.md` file under a
# `specs/` directory. pinned-spec-holds, which keeps a pinned line from changing
# without the user's words, matches exactly those files, so a pin anywhere else
# would name a rule nothing guards. Change the two together.
SPEC_PATH_RE='^(.*/)?SPEC\.md$|^(.*/)?specs/.+\.md$'

# parse_pin <fqn>: sets pin_repo, pin_sha, pin_path, pin_start, pin_end; or sets
# pin_error to why the fqn does not parse and returns 1. Globals, not output, so a
# caller reads them without a subshell.
parse_pin() {
  local fqn="$1" rest range
  pin_repo="${fqn%%@*}"
  rest="${fqn#*@}"
  pin_sha="${rest%%:*}"
  rest="${rest#*:}"
  pin_path="${rest%%#*}"
  range="${rest#*#L}"
  pin_start="${range%-*}"
  pin_end="${range#*-}"

  if [ "$pin_repo" = "$fqn" ] || [ -z "$pin_repo" ] || [ -z "$pin_path" ] || [ "$range" = "$rest" ]; then
    pin_error="Invariant marker '$fqn' does not parse as <repo>@<sha>:<path>#L<start>-<end>."
    return 1
  fi
  if ! printf '%s' "$pin_sha" | grep -Eq '^[0-9a-f]{7,64}$'; then
    pin_error="Invariant marker '$fqn' names '$pin_sha' where a commit sha belongs (7 to 64 hex digits)."
    return 1
  fi
  if ! printf '%s' "$range" | grep -Eq '^[0-9]{1,9}-[0-9]{1,9}$' || [ "$pin_start" -lt 1 ] || [ "$pin_start" -gt "$pin_end" ]; then
    pin_error="Invariant marker '$fqn' pins the range L$range, which is not L<start>-<end> with 1 <= start <= end."
    return 1
  fi
  while [ "${pin_path#./}" != "$pin_path" ]; do pin_path="${pin_path#./}"; done
  case "/$pin_path/" in
    */../* | */./* | //*)
      pin_error="Invariant marker '$fqn' pins the path '$pin_path'; write it repository-relative, without '.' or '..' steps."
      return 1
      ;;
  esac
  if ! printf '%s' "$pin_path" | grep -Eq "$SPEC_PATH_RE"; then
    pin_error="Invariant marker '$fqn' pins '$pin_path', which is not a spec file. Pin the rule where this project keeps its specs — SPEC.md, or a .md file under specs/ — the only files whose pinned lines are guarded from changing."
    return 1
  fi
}

# resolve_pin_sha: sets pin_commit to the full commit pin_sha names; or sets
# pin_error and returns 1 when it names none, or resolves to a commit it is not a
# prefix of (a ref of that name shadowing the sha).
resolve_pin_sha() {
  pin_commit="$(git -C "$pin_repo" rev-parse -q --verify "$pin_sha^{commit}" 2>/dev/null)"
  case "$pin_commit" in
    "$pin_sha"*) [ -n "$pin_commit" ] && return 0 ;;
  esac
  if [ -n "$pin_commit" ]; then
    pin_error="'$pin_sha' resolves to $pin_commit, not a commit it is a prefix of (a branch or tag of that name shadows the sha); use the full sha"
  else
    pin_error="$pin_repo has no commit $pin_sha"
  fi
  return 1
}

# pin_lines <rev>: sets pin_text to lines pin_start..pin_end of pin_path at <rev>
# (pin_sha is resolved first, see resolve_pin_sha);
# or sets pin_error to why they cannot be read and returns 1 — the path is missing
# at <rev>, the file ends before pin_end, or the range holds no text.
pin_lines() {
  local rev="$1" blob total
  pin_text=""
  if [ "$rev" = "$pin_sha" ]; then
    resolve_pin_sha || return 1
    rev="$pin_commit"
  fi
  if ! blob="$(git -C "$pin_repo" cat-file blob "$rev:$pin_path" 2>/dev/null)"; then
    pin_error="there is no $pin_path at $rev in $pin_repo"
    return 1
  fi
  total="$(printf '%s\n' "$blob" | awk 'END { print NR }')"
  if [ "$total" -lt "$pin_end" ]; then
    pin_error="$pin_path at $rev has $total line(s), so L$pin_start-$pin_end is past its end"
    return 1
  fi
  pin_text="$(printf '%s\n' "$blob" | sed -n "${pin_start},${pin_end}p")"
  if [ -z "$(printf '%s' "$pin_text" | tr -d '[:space:]')" ]; then
    pin_error="L$pin_start-$pin_end of $pin_path at $rev is blank"
    return 1
  fi
}
