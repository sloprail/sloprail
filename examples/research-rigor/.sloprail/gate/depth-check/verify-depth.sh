#!/usr/bin/env bash
# The actual depth check, run only while research-run is active. Three
# things the raw unit names: real page count, an actual clone (not just a
# README fetch), keywords logged as the agent went. Page count is read off
# real gh CLI invocations (--limit/--paginate are explicit arguments the
# agent had to type), not inferred from unrelated WebFetch/Read calls.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

block() {
  echo "$1" >&2
  exit 1
}

# 1. Did a real clone happen — not just a README/single-file fetch? A repo
# clone leaves multiple files/dirs; a README fetch leaves one.
clone_cmd="$(sr-session query \
  --transcript "$transcript_path" \
  --select tool_use \
  --where 'any(invocations, .bin == "git" and "clone" in .argv)' \
  | jq -r '.[-1].output // ""')"

if [ -z "$clone_cmd" ]; then
  block "No git clone found in this research run's trajectory — a README fetch alone does not establish depth."
fi

# 2. Page count: read off the real `gh` CLI invocations, not a WebFetch/Read
# tally — a `gh` command carries its own page count as an argument
# (--limit N, --paginate), so this reads what the agent actually asked for
# rather than inferring depth from unrelated tool calls.
gh_invocations="$(sr-session query \
  --transcript "$transcript_path" \
  --select tool_use \
  --where 'any(invocations, .bin == "gh")' \
  | jq -c '[.[].invocations[] | select(.bin == "gh")]')"

if [ "$(printf '%s' "$gh_invocations" | jq 'length')" -eq 0 ]; then
  block "No gh CLI calls found in this research run — nothing establishes how many pages were actually covered."
fi

total_pages="$(printf '%s' "$gh_invocations" | jq '
  [ .[]
    | if (.flags.paginate != null) then 999999          # --paginate: unbounded, counts as satisfying any minimum
      elif (.flags.limit != null) then (.flags.limit | tonumber)
      else 1                                              # a bare gh call with no explicit limit = one page
      end
  ] | add
')"

MIN_PAGES=5
if [ "${total_pages:-0}" -lt "$MIN_PAGES" ]; then
  block "gh CLI calls this run cover only $total_pages page(s) (via --limit/--paginate), below the minimum of $MIN_PAGES."
fi

# 3. Were keywords actually logged, not just claimed? The registry lives in
# sr-session state under the sibling keyword-coverage-registry gate's own
# name, if that guardrail is installed alongside this one — this only
# confirms at least one keyword:<term>:<trajectory-id> entry exists for THIS
# run; whether coverage is ENOUGH, and each term has its own gh search, is
# that sibling gate's own separate test (unit 10's own words: "a SEPARATE
# test from raw depth" — not duplicated here).
logged="$(sr-session state list --owner keyword-coverage-registry 2>/dev/null | jq '[.[] | select(.key | startswith("keyword:"))] | length' 2>/dev/null || echo 0)"

if [ "$logged" -eq 0 ]; then
  block "No keywords were logged to the registry during this research run — depth claimed but not recorded."
fi

# 4. Unit 11 (separate-agent-per-trajectory) folded in here rather than
# kept as its own gate (his correction, 2026-08-19: "why not part of depth
# check? in general it's only applicable f/ subagent trajectories:
# basically happened in subagent AND depth is xyz"). Only meaningful when
# this research run happened INSIDE a subagent trajectory — a main-line
# research turn has no sibling trajectories to isolate from. Secondary per
# the unit's own author ("не так критичен").
#
# TODO(sr-session query): `--select meta` / `--select sibling_trajectories`
# below are not real selectors yet — sub-agent trajectory identification and
# sibling enumeration are unresolved (his own open question on enter.sh:
# "unclear that it's a subagent traj" — part of the deferred
# trajectory-processing action item, decision 20260818_no-slop-primitives
# Thread 1). This block is the shape the check will take once that lands,
# not working code today.
is_subagent="$(sr-session query \
  --transcript "$transcript_path" \
  --select meta \
  | jq -r '.isSubagent // false' 2>/dev/null)"

if [ "$is_subagent" = "true" ]; then
  sibling_count="$(sr-session query \
    --transcript "$transcript_path" \
    --select sibling_trajectories \
    | jq 'length' 2>/dev/null || echo 0)"

  if [ "${sibling_count:-0}" -gt 0 ]; then
    block "This research ran in a subagent trajectory alongside ${sibling_count} sibling trajectories — each research trajectory must run as its own separate agent, not share one with others."
  fi
fi

exit 0
