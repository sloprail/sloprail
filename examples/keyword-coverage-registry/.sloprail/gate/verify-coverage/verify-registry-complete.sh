#!/usr/bin/env bash
# The registry table IS sr-session state (2026-08-19 slice 8) — this gate's
# own name is its SR_GUARDRAIL, so `state list` reads back every keyword
# logged this run. Entries are written directly by the agent as it works
# (his settled pattern for this kind of agent-authored registry, 2026-08-19:
# "let the agent just use that sr session state" — same as intake's skip
# list, no separate tag or context), one call per keyword found:
#
#   sr-session state set "keyword:<term>:<trajectory-id>" "covered"
#
# The VALUE is not load-bearing here (unlike unit 05's citations) — the key
# alone names what to check, and check 3 below verifies coverage by finding
# the term's own `gh` search invocation in that trajectory, not by trusting
# a stored pointer (his correction, 2026-08-19: a bare transcript pointer
# "only proves the WORD appears somewhere", not that a search for it
# actually ran).
#
# Two-level, not flat (his correction, 2026-08-19, after an earlier draft
# treated the table as keyword -> single pointer): a competitive-research
# run can fan out into SEVERAL trajectories, one per competitor/angle (unit
# 11's shape, folded into research-rigor's depth-check as a secondary
# concern there). The registry key therefore carries BOTH axes —
# keyword:<term>:<trajectory-id> — so the same term logged by two different
# trajectories is two entries, not one overwriting the other. This check
# verifies:
#   1. the table is non-empty
#   2. enough DISTINCT keywords were covered (the raw unit's own "enough
#      keywords" test, cross-checked here rather than only in depth-check)
#   3. every keyword has a companion `gh` search invocation that actually
#      searched FOR that term — his correction, 2026-08-19: "close to this
#      like GitHub search with pages... it kind of needs to ensure that all
#      these keywords... actually had their companion GitHub search, like
#      GitHub gh CLI commands to move the actual search query" — same
#      real-command grounding as research-rigor's own page-count check
#      (--limit/--paginate as literal typed arguments), not a bare
#      transcript pointer that only proves the WORD appears somewhere
#   4. every trajectory this run spawned logged at least one keyword — a
#      trajectory that ran and covered nothing is itself a gap the flat
#      "table non-empty" check in an earlier draft could not see
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

entries="$(sr-session state list 2>/dev/null)"
count="$(printf '%s' "$entries" | jq 'length' 2>/dev/null || echo 0)"

if [ "$count" -eq 0 ]; then
  echo "No keywords were logged to the registry this run — coverage cannot be verified against an empty table." >&2
  exit 1
fi

# Every keyword:<term>:<trajectory-id> key — the value itself isn't read.
registry_keys="$(printf '%s' "$entries" | jq -r '[.[] | select(.key | startswith("keyword:"))] | .[].key')"

if [ -z "$registry_keys" ]; then
  echo "sr-session state has entries but none are keyword:<term>:<trajectory-id> registry rows — nothing here is the coverage table this gate checks." >&2
  exit 1
fi

# 2. Enough DISTINCT keywords, term axis only (collapsing the trajectory
# axis) — "убедиться в том, что достаточно много ключевых слов было
# покрыто" (raw dictation, line 2027). No target count is specified in the
# unit; MIN_KEYWORDS is a stand-in the same way research-rigor's MIN_PAGES
# is, tune per deployment.
distinct_terms="$(printf '%s' "$registry_keys" | awk -F: '{print $2}' | sort -u)"
distinct_count="$(printf '%s' "$distinct_terms" | grep -c . || true)"

MIN_KEYWORDS=5
if [ "${distinct_count:-0}" -lt "$MIN_KEYWORDS" ]; then
  echo "Only $distinct_count distinct keyword(s) covered this run, below the minimum of $MIN_KEYWORDS." >&2
  exit 1
fi

# 3. Every keyword's companion `gh` search actually happened — read off the
# real gh CLI invocations per trajectory, the same way research-rigor reads
# page count off --limit/--paginate rather than inferring depth from
# unrelated tool calls. A keyword entry whose term never appears as a `gh
# search`/`gh api search/*` query argument anywhere in its own trajectory is
# a word that showed up in the registry without the search that was
# supposed to establish it.
missing_search=""
while IFS=$'\t' read -r term traj_id; do
  [ -z "$term" ] && continue

  traj_path="$transcript_path"
  if [ -n "$traj_id" ] && [ "$traj_id" != "$(basename "$transcript_path" .jsonl)" ]; then
    traj_path="$(dirname "$transcript_path")/${traj_id}.jsonl"
  fi

  if [ ! -f "$traj_path" ]; then
    missing_search="$missing_search $term(trajectory $traj_id not found at $traj_path)"
    continue
  fi

  gh_calls_for_term="$(sr-session query \
    --transcript "$traj_path" \
    --select tool_use \
    --whole-session \
    --where 'any(invocations, .bin == "gh")' \
    | jq -c --arg t "$term" '[.[].invocations[]? | select(.bin == "gh") | select((.argv // []) | any(. == $t or contains($t)))]' 2>/dev/null)"

  if [ "$(printf '%s' "${gh_calls_for_term:-[]}" | jq 'length' 2>/dev/null || echo 0)" -eq 0 ]; then
    missing_search="$missing_search $term"
  fi
done < <(printf '%s' "$registry_keys" | awk -F: '{print $2"\t"$3}')

if [ -n "$missing_search" ]; then
  echo "These registry keywords have no companion 'gh' search invocation for that exact term in their trajectory — logged without the search that was supposed to establish them:$missing_search" >&2
  exit 1
fi

# 4. Every trajectory this run spawned logged at least one keyword.
#
# TODO(sr-session query): enumerating this run's sibling trajectories is not
# a real selector yet (same open gap research-rigor's own depth-check
# flags in its check 4 — "sibling_trajectories... unresolved", decision
# 20260818_no-slop-primitives Thread 1). Until that lands, this only checks
# the trajectory IDs the registry itself names (from the entries above),
# not whether some OTHER spawned trajectory logged nothing at all — a
# silent gap this check cannot yet see.
covered_trajectories="$(printf '%s' "$registry_keys" | awk -F: '{print $3}' | sort -u | grep -c . || true)"

if [ "${covered_trajectories:-0}" -eq 0 ]; then
  echo "No trajectory IDs found on any registry entry — keyword:<term>:<trajectory-id> rows must name which trajectory covered them." >&2
  exit 1
fi

exit 0
