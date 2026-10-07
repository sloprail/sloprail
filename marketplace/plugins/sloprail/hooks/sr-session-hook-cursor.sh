#!/bin/sh
# Cursor's entrypoint for the same lifecycle points hooks.json gives Claude Code:
# it delegates to sr-session-hook.sh (install checks, auto-install, PATH, the
# missing-engine refusal — all shared), adapting only what differs in Cursor's hook
# contract (harness-mocks cursor-mock recordings, capability hook-exit-code-semantics):
#
#   - sessionStart: stdout is not agent context. Cursor reads a JSON document and
#     treats output that is not valid JSON as invalid; the agent's context is
#     {"additional_context": "..."}. The shared wrapper prints rules-first.md and
#     the load report as plain text, so it is wrapped here.
#   - every other point: exec'd unchanged. A refusal is exit status 2 (Cursor blocks
#     on it too, recorded) or {"permission":"deny"} JSON; what sr-session itself
#     writes to stdout is the engine's concern, not this wrapper's.
set -eu

subcommand="$1"
here="$(dirname "$0")"

if [ "$subcommand" = "start" ]; then
  out="$(sh "$here/sr-session-hook.sh" "$@" || true)"
  # JSON-escape the text with awk (jq is not guaranteed on a machine Cursor runs on).
  body="$(printf '%s' "$out" | awk 'BEGIN { ORS = "" } { gsub(/\\/, "\\\\"); gsub(/"/, "\\\""); gsub(/\t/, "\\t"); gsub(/\r/, ""); if (NR > 1) print "\\n"; print }')"
  printf '{"additional_context":"%s"}\n' "$body"
  exit 0
fi

exec sh "$here/sr-session-hook.sh" "$@"
