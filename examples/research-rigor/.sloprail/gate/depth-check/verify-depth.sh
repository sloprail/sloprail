#!/usr/bin/env bash
# Depth check: an actual clone happened (not just a README fetch) and gh CLI
# calls covered enough pages. Keyword coverage is a separate gate, not checked here.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

block() {
  echo "$1" >&2
  exit 1
}

# 1. A real clone, recognised by the command: a PreCommandInvoke `git` with
# `clone` in argv. Count invocations, not captured output — .toolUseResult lives
# on the result record, not the tool_use record this event rides, so it is always
# empty here and would refuse even a real clone.
clone_count="$(sr-session trajectory normalize \
  --path "$transcript_path" \
  --events PreCommandInvoke \
  | jq '[ .[]
      | select(any(.events[]?; .kind == "PreCommandInvoke"
          and any(.invocations[]?; .bin == "git" and any(.argv[]?; . == "clone"))))
    ] | length')"

if [ "${clone_count:-0}" -eq 0 ]; then
  block "No git clone found in this research run's trajectory — a README fetch alone does not establish depth."
fi

# 2. Page count from gh CLI invocations, which carry it as an argument
# (--limit N, --paginate). Events are flat: `.invocations` sits beside `.kind`.
gh_invocations="$(sr-session trajectory normalize \
  --path "$transcript_path" \
  --events PreCommandInvoke \
  | jq -c '[ .[] | .events[]? | select(.kind == "PreCommandInvoke")
             | .invocations[]? | select(.bin == "gh") ]')"

if [ "$(printf '%s' "$gh_invocations" | jq 'length')" -eq 0 ]; then
  block "No gh CLI calls found in this research run — nothing establishes how many pages were actually covered."
fi

total_pages="$(printf '%s' "$gh_invocations" | jq '
  # The page count one gh call asks for: --paginate is unbounded; --limit N
  # is N (read from the flag value, or from the argv token right after
  # --limit/-L when the value was space-separated and so landed in argv); a
  # bare call with no explicit limit is one page.
  def limit_from_argv:
    (.argv // []) as $a
    | ( [ range(0; ($a | length)) | select($a[.] == "--limit" or $a[.] == "-L") | $a[.+1] ] | .[0] // "" );
  [ .[]
    | if (.flags.paginate != null) then 999999
      elif (((.flags.limit // [])[-1] // "") | test("^[0-9]+$")) then ((.flags.limit // [])[-1] | tonumber)
      elif (limit_from_argv | test("^[0-9]+$")) then (limit_from_argv | tonumber)
      else 1
      end
  ] | add
')"

MIN_PAGES=5
if [ "${total_pages:-0}" -lt "$MIN_PAGES" ]; then
  block "gh CLI calls this run cover only $total_pages page(s) (via --limit/--paginate), below the minimum of $MIN_PAGES."
fi

# 3. Keyword coverage is a separate gate (keyword-coverage-registry), not here.

# 4. Each research trajectory must be its own agent. Only meaningful inside a
# subagent run: refuse when this ran as a subagent (.isSubagent) that carries
# sibling trajectory paths (.subagentPaths) alongside it.
if ! facts="$(sr-session trajectory describe --path "$transcript_path" 2>&1)"; then
  block "Could not describe this research run's trajectory ($transcript_path), so whether it ran as its own agent is unknown: $facts"
fi
# Checked by VALUE, not jq's exit status: some jq builds exit 0 on unparseable
# or empty input, which would read as "not a subagent" and permit.
is_subagent="$(printf '%s' "$facts" | jq -r '.isSubagent' 2>/dev/null)"
case "$is_subagent" in
  true | false) ;;
  *) block "trajectory describe did not report isSubagent for $transcript_path, so whether this research ran as its own agent is unknown." ;;
esac

if [ "$is_subagent" = "true" ]; then
  sibling_count="$(printf '%s' "$facts" | jq '(.subagentPaths // []) | length' 2>/dev/null)"
  case "$sibling_count" in
    '' | *[!0-9]*) block "trajectory describe returned unreadable subagentPaths for $transcript_path, so sibling trajectories could not be counted." ;;
  esac

  if [ "$sibling_count" -gt 0 ]; then
    block "This research ran in a subagent trajectory alongside ${sibling_count} sibling trajectories — each research trajectory must run as its own separate agent, not share one with others."
  fi
fi

exit 0
