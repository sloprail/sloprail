#!/usr/bin/env bash
# The gate's match already decided: an agent ran a hook-only entry point of sr-session.
# Contract: stdin is the GateCheckPayload; exit 1 with {"reason": ...} refuses.
set -uo pipefail

cat >/dev/null
jq -n '{reason: "sr-session start / stop / pre-tool / subagent-stop / worktree-remove are the harness hooks'"'"' entry points, not commands for an agent: they take a real session on stdin, and run by hand they invent a session and record verdicts the real one never sees. Do not run them, and do not write a judge of your own (a script that calls `claude -p` to grade your work). The Stop hook judges what you committed when you end your turn; to read state use the read-only commands (`sr-session trajectory`, `sr-session refs list`, `sr-checks status`, `sr-checks sql`). A rule that does not load is reported to you at the next hook and at Stop; you never need to run `start` to check."}'
exit 1
