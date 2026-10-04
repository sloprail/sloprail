#!/usr/bin/env bash
# Sourced by pin-still-matches-head.sh and pinned-text.sh, so both read a pin the
# same way. An sr:invariant marker's fqn is a pinned spec reference:
#   <repo>@<sha>:<path>#L<start>-<end>
# The fqn is written by the agent being judged, so nothing in it reaches git
# unchecked:
#
#   - The sha must be a FULL commit id (40 hex digits, or 64 in a SHA-256
#     repository), and it is read as an object id only. A short sha resolves
#     through refs first, so a branch or tag named like it would stand in for it;
#     and an option in its place (`--output=<file>`) would have git write a file.
#   - <repo> must be this project's own repository: a pin into another checkout
#     names text nothing here guards.
#   - The path must be a spec: `SPEC.md` at any depth, or a `.md` file under a
#     `specs/` directory, case-insensitively (macOS file systems are). That is
#     what pinned-spec-holds matches, so a pin anywhere else would name a rule
#     nothing guards. Change the two together (a test holds them in step).
#   - The range must be a real one inside the file: a range past its end pins no
#     text, and a judge handed an empty <pinned> rules against nothing.
SPEC_PATH_RE='^(.*/)?spec\.md$|^(.*/)?specs/.+\.md$'

# Pins name objects; a replace ref (refs/replace/<sha>) must not swap another in.
export GIT_NO_REPLACE_OBJECTS=1

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
  if ! printf '%s' "$pin_sha" | grep -Eq '^([0-9a-f]{40}|[0-9a-f]{64})$'; then
    pin_error="Invariant marker '$fqn' names '$pin_sha' where a full commit sha belongs (40 hex digits: git log -1 --format=%H -- <spec>). A short sha resolves through branch and tag names first."
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
  if ! printf '%s' "$pin_path" | grep -Eiq "$SPEC_PATH_RE"; then
    pin_error="Invariant marker '$fqn' pins '$pin_path', which is not a spec file. Pin the rule where this project keeps its specs — SPEC.md, or a .md file under specs/ — the only files whose pinned lines are guarded from changing."
    return 1
  fi
}

# resolve_pin_sha: checks pin_repo is this project's repository and pin_sha names
# a commit in it, read as an object id; sets pin_commit. Otherwise sets pin_error
# and returns 1.
resolve_pin_sha() {
  local here there
  here="$(git -C "${SR_WORKSPACE:-.}" rev-parse --show-toplevel 2>/dev/null)"
  there="$(git -C "$pin_repo" rev-parse --show-toplevel 2>/dev/null)"
  here="$(cd "$here" 2>/dev/null && pwd -P)"
  there="$(cd "$there" 2>/dev/null && pwd -P)"
  if [ -z "$there" ] || [ "$here" != "$there" ]; then
    pin_error="the pin names the repository '$pin_repo', which is not this project's ($here); pin this project's own spec"
    return 1
  fi
  if [ "$(git -C "$pin_repo" cat-file -t "$pin_sha" 2>/dev/null)" != commit ]; then
    pin_error="$pin_repo has no commit $pin_sha"
    return 1
  fi
  pin_commit="$pin_sha"
}

# pin_lines <rev>: sets pin_text to lines pin_start..pin_end of pin_path at <rev>
# (pin_sha is checked first, see resolve_pin_sha); or sets pin_error to why they
# cannot be read and returns 1 — the path is missing at <rev>, the file ends
# before pin_end, or the range holds no text.
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

# LOADED SENTINEL — keep this the LAST line. bash runs a sourced file up to its
# first syntax error, so a helper can load partly; a caller unsets this,
# sources, and checks it, which proves the whole file ran.
pin_loaded=1
