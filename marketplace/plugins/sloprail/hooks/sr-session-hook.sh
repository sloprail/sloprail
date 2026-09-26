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
# and prints the resolved path on stdout if found. It does not itself modify
# $PATH; the one directory it finds is added below, for the engine's own
# siblings, and nothing else is widened.
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

# First session after `/plugin install`: the plugin installs the sr* binaries
# itself, once, so installing the plugin is the whole install. Asking the agent
# to do it was measured to fail both ways: a careful model (rightly) will not
# pipe a script from a hook's message into sh without asking, and a careless
# one disables the plugin to get past the refusal.
#
# It is the pattern package tools use for a runtime they fetch on first use
# (Prisma's engines, Playwright's browsers), and it is held to the same bar:
#   pinned    — the release matching THIS plugin's version (they are bumped in
#               lockstep, scripts/bump-version.sh), never "latest";
#   verified  — install.sh checks the archive against the release's
#               checksums.txt and refuses a mismatch;
#   visible   — announced in the session, success or failure;
#   opt-out   — SLOPRAIL_NO_AUTO_INSTALL=1 leaves it to a manual install.sh.
# Only at start: it is the one hook point with time for a download, and the
# session learns the result before its first tool call. install.sh here is a
# byte-identical copy of the repository's (a test holds them equal), since a
# plugin can only run what it ships.
if [ -z "$sr_session_bin" ] && [ "$subcommand" = "start" ] && [ -z "${SLOPRAIL_NO_AUTO_INSTALL:-}" ]; then
  version="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$(dirname "$0")/../.claude-plugin/plugin.json" 2>/dev/null | head -1)"
  tag="${SLOPRAIL_INSTALL_TAG:-v${version}}"
  echo "sloprail: installing the sr binaries (${tag}) into ${SLOPRAIL_INSTALL_DIR:-~/.local/bin}, one time. Set SLOPRAIL_NO_AUTO_INSTALL=1 to install by hand instead." | tee /dev/stderr
  install_log="$(mktemp)"
  if SLOPRAIL_INSTALL_TAG="$tag" sh "$(dirname "$0")/install.sh" </dev/null >"$install_log" 2>&1; then
    sr_session_bin="$(find_sr_session)" || sr_session_bin=""
  fi
  if [ -n "$sr_session_bin" ]; then
    echo "sloprail: installed ${tag}: ${sr_session_bin}" | tee /dev/stderr
  else
    echo "sloprail: the automatic install failed; guardrails are NOT enforcing until it is fixed:" | tee /dev/stderr
    tail -5 "$install_log" | tee /dev/stderr
  fi
  rm -f "$install_log"
fi

# The engine runs its sibling binaries BY NAME — a judge check execs `sr-agent`,
# and a shipped check script may too. find_sr_session may have found the set in
# a directory that is not on the hook's $PATH (~/.local/bin, the install.sh
# default, usually is not), and then every judge failed with "sr-agent: command
# not found" while sr-session itself ran fine: measured on a fresh install,
# where the plugin's own authoring-slop refused every rule write until the agent
# disabled it. So the directory the set was found in goes first on $PATH for
# the engine — only that directory, which holds nothing but sloprail's binaries.
saved_path="$PATH"
if [ -n "$sr_session_bin" ]; then
  PATH="$(dirname "$sr_session_bin"):$PATH"
  export PATH
fi

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
  # The notice tells the agent to run sr-session; when the set was found off
  # the agent's $PATH (~/.local/bin usually is), a bare `sr-session` is
  # "command not found" — measured in most fresh-install runs. Say where it is.
  if [ -n "$sr_session_bin" ] && [ "$(PATH="$saved_path" command -v sr-session 2>/dev/null)" != "$sr_session_bin" ]; then
    echo
    echo "sr-session is not on \$PATH here: it is $sr_session_bin (the whole sr* set is beside it)."
  fi
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
    #
    # HOW it blocks is Claude Code's hook contract, not the engine's check
    # contract: a PreToolUse hook refuses only by exiting 2, and only then is
    # stderr fed to the agent. Any other non-zero is a "non-blocking error" —
    # the tool runs and the agent never sees why. This used to exit 1, and a
    # real fresh-install run showed every tool call going through with the
    # message shown to nobody: the silent no-op this wrapper exists to prevent.
    #
    # WHAT it blocks is file writes (Write/Edit/MultiEdit/NotebookEdit), not
    # every tool: refusing Bash too would refuse the very command that
    # installs sr-session, a session that can never repair itself. A write is
    # where the rules would have applied, so that is where the agent hears it.
    payload="$(cat)"
    tool="$(printf '%s' "$payload" | tr -d '\n' | sed -n 's/.*"tool_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
    case "$tool" in
    Write | Edit | MultiEdit | NotebookEdit)
      echo "BLOCKED: sloprail cannot check this write because its sr-session binary is not installed. Install it (from a shell), then retry:" >&2
      echo "  ${install_hint}" >&2
      echo "(sloprail/sloprail may be private: if that URL 404s, fetch install.sh with your git/gh access and run it.)" >&2
      exit 2
      ;;
    *)
      echo "$message" >&2
      exit 0
      ;;
    esac
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

# At start, the load report (a rule that did not load, a shadowed one, an
# unresolved plugin) is written to stderr, for the person. But in a rules-first
# session the AGENT wrote those rules, and a rule that silently failed to load
# is one it believes is enforcing. So at start only, the report is repeated on
# stdout — the agent's context — under a line saying what it means. stdin (the
# hook payload) passes straight through; the exit status is sr-session's.
if [ "$subcommand" = "start" ]; then
  report="$(mktemp)"
  status=0
  "$sr_session_bin" start "$@" 2>"$report" || status=$?
  cat "$report" >&2
  # Everything but the two lines that are bookkeeping, not a problem with a
  # rule: which plugin owns which folders, and a baseline that could not be
  # recorded.
  problems="$(grep -v ' owns \|no baseline recorded' "$report" 2>/dev/null || true)"
  if [ -n "$problems" ]; then
    echo
    echo "sloprail load check: what follows is NOT in force until fixed (the sloprail:authoring-guardrails skill has the format; re-check with: sr-session start < /dev/null)"
    printf '%s\n' "$problems"
  fi
  rm -f "$report"
  exit "$status"
fi

exec "$sr_session_bin" "$subcommand" "$@"
