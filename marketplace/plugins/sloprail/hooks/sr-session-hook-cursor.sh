#!/bin/sh
# Cursor's entrypoint for the same lifecycle points hooks.json gives Claude Code:
# it delegates to sr-session-hook.sh (install checks, auto-install, PATH, the
# missing-engine refusal — all shared), only naming the harness first.
#
# Nothing here adapts Cursor's hook contract: what sr-session writes for a refusal
# or for the session's start-up context is the running harness's own form
# (harness.RenderHook — Cursor's sessionStart context is {"additional_context"},
# a refusal is {"permission":"deny"} or exit status 2; harness-mocks cursor-mock
# recordings), and the shared wrapper hands its start-up text to
# `sr-session emit-context` for exactly that.
set -eu

# Name the harness outright: the engine's hook parser and responses are the running
# harness's (internal/harness Current), and the environment alone does not say it is
# Cursor (CURSOR_VERSION / CURSOR_PROJECT_DIR also appear in an editor's terminal).
SLOPRAIL_HARNESS=cursor
export SLOPRAIL_HARNESS

# Cursor tells a hook which plugin it belongs to (CURSOR_PLUGIN_ROOT) only when the hook
# is the plugin's own. The stop and sessionStart hooks run from the project's
# .cursor/hooks.json (a plugin's never fire), where it is not set, and the engine finds
# the plugin's shipped guardrails from it: so a hook run by path names its own plugin.
CURSOR_PLUGIN_ROOT="${CURSOR_PLUGIN_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
export CURSOR_PLUGIN_ROOT

exec sh "$(dirname "$0")/sr-session-hook.sh" "$@"
