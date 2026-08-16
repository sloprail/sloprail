#!/bin/sh
# Refuses a write whose target looks like a secret.
#
# WHAT THIS IS AN EXAMPLE OF. A plugin that registers its OWN PreToolUse hook and
# does its own deciding — no GUARDRAIL.md, no declaration for the engine to
# dispatch. The harness's lifecycle is the cycle; this script is the rule.
#
# It knows what the turn is about to do because `sr-file changes` tells it. That
# is the whole reason the command exists: the payload here is the HARNESS's, so
# the tool arguments are raw, and working out "which file, and what bytes" from
# them means re-deriving what the engine already knows how to do — including for
# a shell command, where the target has to be parsed out of the line rather than
# read off a field.
#
# EXIT 2 REFUSES. Not 1, and this is the one thing about writing a PreToolUse
# hook that is easy to get wrong in the permissive direction: Claude Code blocks
# a tool call on exit code 2 SPECIFICALLY. Any other non-zero status is read as
# "the hook ran and had nothing to say", so the tool call PROCEEDS and the
# refusal reaches nobody.
#
# Measured while writing this plugin: with `exit 1` on every refusing path, the
# e2e wrote secrets.md and passed the write straight through, with the refusal
# text going nowhere. The hook looked installed, ran on every write, and enforced
# nothing.
#
# This is Claude Code's contract rather than sloprail's, because nothing of
# sloprail's is dispatching this hook — the harness is the cycle here. A rule
# declared as a GUARDRAIL is dispatched by the engine and uses the engine's
# convention (any non-zero refuses) instead.

set -u

payload="$(cat)"

# The one dependency beyond POSIX sh, checked by name before any use. Every jq
# call below is 2>/dev/null-suppressed, so without it the selection silently
# comes back empty and the script PERMITS — a plugin's missing dependency
# reading as approval, which is the failure this project exists to prevent.
command -v jq >/dev/null 2>&1 || {
  echo "sloprail-secrets: needs jq, which is not on PATH. Refusing rather than permitting unchecked." >&2
  exit 2
}
command -v sr-file >/dev/null 2>&1 || {
  echo "sloprail-secrets: needs sr-file, which is not on PATH. Refusing rather than permitting unchecked." >&2
  exit 2
}

# What the turn is about to do, as JSONL — one row per file, whatever the tool.
#
# Selecting is jq's job rather than a matcher language's: the rows are JSON and
# a consumer already knows jq. The pattern is deliberately narrow (a path
# component containing "secret") so the negative control in the e2e is a real
# control rather than a path this happens not to match.
hits="$(printf '%s' "$payload" | sr-file changes 2>/dev/null \
  | jq -r 'select(.path | test("secret")) | "\(.kind) \(.path)"' 2>/dev/null)"

[ -n "$hits" ] || exit 0

cat >&2 <<EOF
SECRETS: this turn would write to a path that looks like a secret.

$hits

Renaming the file is not the fix — put the value somewhere it is not committed,
and reference it. If this path is genuinely not a secret, the pattern this rule
matches on lives in the plugin, not in your project.
EOF
exit 2
