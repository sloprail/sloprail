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

install_hint='curl -fsSL https://sloprail.com/install.sh | sh'

if ! command -v sr-session >/dev/null 2>&1; then
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
    exit 0
    ;;
  esac
fi

exec sr-session "$subcommand" "$@"
