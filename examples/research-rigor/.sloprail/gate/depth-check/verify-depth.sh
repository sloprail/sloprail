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
# name, if that guardrail is installed alongside this one; here we only
# confirm at least one entry exists for THIS run.
logged="$(sr-session state list --owner keyword-coverage-registry 2>/dev/null | jq 'length' 2>/dev/null || echo 0)"

if [ "$logged" -eq 0 ]; then
  block "No keywords were logged to the registry during this research run — depth claimed but not recorded."
fi

exit 0
