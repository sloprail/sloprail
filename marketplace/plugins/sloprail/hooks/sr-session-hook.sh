#!/bin/sh
# Wraps `sr-session <subcommand>` for every lifecycle point in hooks.json, so a
# missing install is a loud failure rather than a silent no-op.
#
# THE BUG THIS FIXES: hooks.json used to call `sr-session <subcommand>` bare.
# `/plugin install` registers these hooks in seconds, but nothing on that path
# puts the `sr*` binaries anywhere — they come from a SEPARATE install a
# stranger is never told to run. Without them, the harness tries to exec a
# command that is not on $PATH, that failure is not this plugin's hook erroring
# (it never started), and the harness moves on. The agent runs completely
# unguarded: no error, no warning, nothing in the transcript. That is the
# worst possible first impression, and it directly contradicts the project's
# own rule (see .sloprail/file-guard/authoring-slop/rules/
# a-hook-that-could-not-run-has-not-permitted/RULE.md): a hook that could not
# run has not permitted.
#
# THE FIX IS HERE, not in sr-session, because sr-session missing is exactly
# the case where sr-session cannot be the one to notice. This wrapper is
# POSIX sh with no dependency beyond `command`, so it runs on every machine
# Claude Code itself runs on, and it is the one thing standing between the
# harness and a binary that may not exist.
#
# WHY THIS ALSO CHECKS BEYOND BARE $PATH. A hook's environment is the
# harness's own, and Claude Code's hook environment is not guaranteed to
# carry every directory a user's interactive shell profile adds to $PATH —
# in particular install.sh's own default destination, ~/.local/bin, is a
# common case that is NOT on a hook's PATH on a real machine. Refusing there
# would block a user who installed correctly, indistinguishable from one who
# never installed at all — the same silent-feeling failure this wrapper
# exists to prevent, just one layer further in. So before concluding
# sr-session is missing, this also tries, in order: $SLOPRAIL_INSTALL_DIR
# (an explicit override, if the user set one), ~/.local/bin (install.sh's
# default), and $(go env GOPATH)/bin or ~/go/bin (go install's default) —
# every directory THIS project's own documented install paths can put the
# binary in. Only when none of them has it does this refuse/warn.
#
# WHY BLOCK ON pre-tool BUT NOT ON THE OTHERS. The project's stance is
# fail-closed, but bricking Claude Code entirely over a missing install is a
# worse first impression than the bug being fixed — a session that cannot
# start, and cannot ever end a turn, because a plugin failed to install
# correctly, teaches a stranger to uninstall sloprail, not to fix their PATH.
# So only the one moment that is actually a GUARDED action — pre-tool, where a
# real rule would have refused something — fails closed. start, stop and
# subagent-stop are not themselves guarded actions (no tool call, no turn
# commit is being decided there); they warn loudly on stderr and permit, which
# is the SessionStart message a person reads once at the top of the session
# and the same warning repeated at every turn boundary so it cannot scroll by
# unnoticed. This mirrors the project's own asymmetry for an unresolved
# plugin (internal/harness/claudecode.go Unresolved.Message): a rule that
# cannot be located is reported, not blocked, because nothing is yet known to
# depend on it — here nothing is yet known to depend on the missing binary
# either, until pre-tool, which is the one moment sloprail would otherwise be
# silently not enforcing something.
set -eu

subcommand="$1"
shift

install_hint='curl -fsSL https://raw.githubusercontent.com/sloprail/sloprail/main/install.sh | sh'

# find_sr_session looks past bare $PATH, in the fixed order documented above,
# and prints the resolved path on stdout if found. It does not modify $PATH —
# a directory found here is used for THIS exec only, so a session that never
# sees an interactive shell's profile still finds a correctly-installed
# binary without this wrapper silently widening the environment for
# anything else the hook chain runs.
find_sr_session() {
  if command -v sr-session >/dev/null 2>&1; then
    command -v sr-session
    return 0
  fi

  candidates=""
  [ -n "${SLOPRAIL_INSTALL_DIR:-}" ] && candidates="$candidates $SLOPRAIL_INSTALL_DIR"
  [ -n "${HOME:-}" ] && candidates="$candidates $HOME/.local/bin"

  # || gobin="" matters under `set -e`: unlike a bare `[ ] && x=...` statement
  # (exempt from -e as a non-final element of a && list), a failing command
  # INSIDE a command substitution used as an assignment's RHS propagates its
  # exit status to the assignment itself, and `set -e` aborts the whole
  # script right here — silently, since nothing has been printed yet. Proven
  # by removing this guard: with no `go` on PATH, the wrapper exits 1 before
  # reaching its own missing-binary message, which is worse than the bug this
  # whole script exists to fix.
  gobin="$(command -v go >/dev/null 2>&1 && go env GOPATH 2>/dev/null)" || gobin=""
  [ -n "$gobin" ] && candidates="$candidates $gobin/bin"
  [ -n "${HOME:-}" ] && candidates="$candidates $HOME/go/bin"

  for dir in $candidates; do
    if [ -x "$dir/sr-session" ]; then
      echo "$dir/sr-session"
      return 0
    fi
  done

  return 1
}

sr_session_bin="$(find_sr_session)" || sr_session_bin=""

# At session start, stdout is context for the AGENT (stderr is for the person).
# rules-first.md is the one standing instruction this plugin gives the agent:
# repeating work gets its structure and rules before the work, one-offs get
# none and keep their scratch files out of the repo. It is printed here, before
# sr-session runs, and whether or not sr-session is installed — a session on a
# half-finished install is exactly the one that most needs to hear it.
# sr-session start itself writes nothing to stdout (session_start.go), so the
# two never interleave.
if [ "$subcommand" = "start" ]; then
  cat "$(dirname "$0")/rules-first.md" 2>/dev/null || true
fi

if [ -z "$sr_session_bin" ]; then
  message="sloprail: the sr-session binary is not installed (or not on \$PATH), so its guardrails are NOT enforcing.
Install it:
  ${install_hint}
Then start a new session — this one will keep warning until sr-session is found."

  case "$subcommand" in
  pre-tool)
    # The one moment that is a guarded action. A rule that cannot run must not
    # be read as having permitted, so this blocks rather than warns — the
    # engine's own philosophy applied one layer up, to the engine's own
    # absence.
    echo "$message" >&2
    echo "BLOCKED: sloprail cannot enforce its guardrails because sr-session is missing. ${install_hint}" >&2
    # Non-zero refuses (script-checks.md: "exit 0 permits ... echo \"…\" >&2;
    # exit 1 is an ordinary refusal"). This wrapper follows that same
    # convention rather than inventing a different one.
    exit 1
    ;;
  *)
    # start / stop / subagent-stop: not a guarded action by itself. Warn
    # loudly (this is what a person reads at SessionStart) and let the
    # session continue — a missing install must not brick Claude Code
    # entirely, only block the actions sloprail would have been guarding.
    echo "$message" >&2
    # At start the agent hears it too (stdout is its context): it is the one
    # that can finish the install, and otherwise learns of it only from its
    # first blocked tool call.
    [ "$subcommand" = "start" ] && echo "$message"
    exit 0
    ;;
  esac
fi

exec "$sr_session_bin" "$subcommand" "$@"
