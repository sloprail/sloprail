#!/usr/bin/env bash
# The gate's match found sr-checks (or sr) with a `run` argument. Refuse it inside a sub-agent:
# SR_AGENT_ID is the sub-agent's id there, empty in the main session.
# Contract: stdin is the GateCheckPayload; exit 1 with {"reason": ...} refuses.
set -uo pipefail

payload="$(cat)"
[ -n "${SR_AGENT_ID:-}" ] || exit 0

# Only a real `run` subcommand: `sr-checks run …` or `sr checks run …`, not a `run` that is an
# option's value somewhere else in the line.
# Fail closed: a payload jq cannot read is refused, not let through unchecked.
if ! runs="$(printf '%s' "$payload" | jq -e '[.event.invocations[]? | (.bin | split("/") | last) as $b | (.argv // [])[1:] as $a
  | select(($b == "sr-checks" and $a[0] == "run") or ($b == "sr" and $a[0] == "checks" and $a[1] == "run"))] | length')"; then
  echo '{"reason":"no-subagent-check-runs could not read the command this sub-agent runs; refusing rather than letting a possible sr-checks run through. Leave sr-checks run to the main session."}'
  exit 1
fi
[ "$runs" -gt 0 ] || exit 0

jq -n --arg r 'Sub-agents do not run `sr-checks run`: every run starts paid model judges, and only the main session decides when a range is final and worth judging. Finish your change, commit it, and report back; the main session runs the checks once. `sr-checks verify` and `sr-checks show` ask no model and stay allowed.' '{reason: $r}'
exit 1
