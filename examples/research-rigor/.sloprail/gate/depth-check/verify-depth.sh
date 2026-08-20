#!/usr/bin/env bash
# The actual depth check, run only while research-run is active — real page
# count and an actual clone (not just a README fetch). Keyword coverage is
# its own SEPARATE test (unit 10), not duplicated here — see check 3 below.
# Page count is read off real gh CLI invocations (--limit/--paginate are
# explicit arguments the agent had to type), not inferred from unrelated
# WebFetch/Read calls.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

block() {
  echo "$1" >&2
  exit 1
}

# 1. Did a real clone happen — not just a README/single-file fetch? A clone is
# recognised by the COMMAND the agent actually ran: a `git clone` invocation,
# re-derived from the trajectory as a PreCommandInvoke event whose invocation is
# `git` with `clone` in its argv. A README/single-file fetch is a different
# command (curl/WebFetch/gh api on one path), so it does not match — the
# invocation itself is what distinguishes depth from a peek.
#
# 2026-08-20: this counts the git-clone INVOCATIONS rather than reading the
# clone's captured output. An earlier version read the clone entry's
# `.toolUseResult` to prove the clone produced files, but that field lives on
# the tool_RESULT record, not on the assistant tool_use record the
# PreCommandInvoke event rides — so the extraction was always empty and this
# check refused even a real clone. The command's own presence is the honest,
# re-derivable signal (and the only one a trajectory normalize exposes: the
# result records carry no uuid and are not re-emitted as normalized entries).
clone_count="$(sr-session trajectory normalize \
  --path "$transcript_path" \
  --events PreCommandInvoke \
  | jq '[ .[]
      | select(any(.events[]?; .kind == "PreCommandInvoke"
          and any(.fields.invocations[]?; .bin == "git" and any(.argv[]?; . == "clone"))))
    ] | length')"

if [ "${clone_count:-0}" -eq 0 ]; then
  block "No git clone found in this research run's trajectory — a README fetch alone does not establish depth."
fi

# 2. Page count: read off the real `gh` CLI invocations, not a WebFetch/Read
# tally — a `gh` command carries its own page count as an argument
# (--limit N, --paginate), so this reads what the agent actually asked for
# rather than inferring depth from unrelated tool calls.
#
# 2026-08-20: the invocations sit under each event's `.fields.invocations`
# (the normalized event wire form is {kind, fields}), not `.invocations` — an
# earlier draft read `.invocations[]?` off the event and matched nothing. And a
# `--limit N` written as two words captures the flag with an EMPTY value (the
# command parser records `--flag=value` but leaves a space-separated value as a
# separate positional in argv), so the page count reads the number out of argv
# when the flag value is empty, and treats `--paginate` as unbounded.
gh_invocations="$(sr-session trajectory normalize \
  --path "$transcript_path" \
  --events PreCommandInvoke \
  | jq -c '[ .[] | .events[]? | select(.kind == "PreCommandInvoke")
             | .fields.invocations[]? | select(.bin == "gh") ]')"

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
      elif ((.flags.limit // "") | test("^[0-9]+$")) then (.flags.limit | tonumber)
      elif (limit_from_argv | test("^[0-9]+$")) then (limit_from_argv | tonumber)
      else 1
      end
  ] | add
')"

MIN_PAGES=5
if [ "${total_pages:-0}" -lt "$MIN_PAGES" ]; then
  block "gh CLI calls this run cover only $total_pages page(s) (via --limit/--paginate), below the minimum of $MIN_PAGES."
fi

# 3. Keyword coverage itself is NOT re-checked here — it is a SEPARATE test
# (unit 10's own words), owned entirely by the sibling keyword-coverage-
# registry gate, which derives its table straight off the same kind of `gh`
# invocations check 2 just read (review, PR #19 review 4974546461: an
# earlier draft here read an agent-written sr-session state registry that
# nothing actually enforced — dropped once the sibling gate stopped trusting
# a write and started deriving coverage from real gh search calls instead).
# Duplicating that derivation here would be the same check running twice for
# no reason; install both gates together if a project wants both tests.

# 4. Unit 11 (separate-agent-per-trajectory) folded in here rather than
# kept as its own gate (his correction, 2026-08-19: "why not part of depth
# check? in general it's only applicable f/ subagent trajectories:
# basically happened in subagent AND depth is xyz"). Only meaningful when
# this research run happened INSIDE a subagent trajectory — a main-line
# research turn has no sibling trajectories to isolate from. Secondary per
# the unit's own author ("не так критичен").
#
# `sr-session trajectory describe` reports the facts about this trajectory:
# whether it is itself a subagent run (.isSubagent) and the sub-agent
# trajectory paths hanging off it (.subagentPaths). The check is: this research
# ran as a subagent AND carries other trajectory paths alongside it — each
# research trajectory must be its own separate agent, not share one with others.
facts="$(sr-session trajectory describe --path "$transcript_path" 2>/dev/null)"
is_subagent="$(printf '%s' "$facts" | jq -r '.isSubagent // false' 2>/dev/null)"

if [ "$is_subagent" = "true" ]; then
  sibling_count="$(printf '%s' "$facts" | jq '(.subagentPaths // []) | length' 2>/dev/null || echo 0)"

  if [ "${sibling_count:-0}" -gt 0 ]; then
    block "This research ran in a subagent trajectory alongside ${sibling_count} sibling trajectories — each research trajectory must run as its own separate agent, not share one with others."
  fi
fi

exit 0
