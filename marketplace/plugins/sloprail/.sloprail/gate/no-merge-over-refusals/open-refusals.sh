#!/usr/bin/env bash
# `when` of no-merge-over-refusals: exit 0 when the branch a `gh pr merge` would
# merge has open refusals at its tip, OR a tip this session recorded that no rule has
# passed yet (every file-guard that ran needs a finished passing run at or above it), so
# the user-citation requirement applies; exit 1 when neither holds or the branch cannot
# be resolved (nothing to refuse on).
# Prints {"hint": "..."} on stdout: the refusal's own advice.
#
# Contract: stdin is the gate payload; the command's argv is
# .event.invocations[] (bin "gh", argv ["pr","merge",...]).
set -uo pipefail

payload="$(cat)"
argv=()
while IFS= read -r a; do argv+=("$a"); done < <(printf '%s' "$payload" | jq -r '
  [.event.invocations[]? | select(.bin == "gh") | select(any(.argv[]?; . == "merge"))] | first | .argv[]?')

# The positional (a PR number, URL or branch) and the repo, skipping flag values.
spec="" repo="" seen_merge="" skip=""
for a in "${argv[@]}"; do
  if [ -n "$skip" ]; then
    [ "$skip" = repo ] && repo="$a"
    skip=""
    continue
  fi
  case "$a" in
    merge) seen_merge=1 ;;
    -R|--repo) skip=repo ;;
    -b|--body|-F|--body-file|-t|--subject|-A|--author-email|--match-head-commit) skip=other ;;
    --repo=*) repo="${a#--repo=}" ;;
    -*) ;;
    *) [ -n "$seen_merge" ] && [ -z "$spec" ] && spec="$a" ;;
  esac
done

ws="${SR_WORKSPACE:-.}"

# The branch and its tips. gh knows a PR number or URL; failing that the positional
# is a branch name, and with none the current branch is the one merged.
branch="" tips=()
if command -v gh >/dev/null 2>&1; then
  gh_args=(pr view)
  [ -n "$spec" ] && gh_args+=("$spec")
  [ -n "$repo" ] && gh_args+=(-R "$repo")
  if info="$(gh "${gh_args[@]}" --json headRefName,headRefOid 2>/dev/null)"; then
    branch="$(printf '%s' "$info" | jq -r '.headRefName // ""')"
    oid="$(printf '%s' "$info" | jq -r '.headRefOid // ""')"
    [ -n "$oid" ] && tips+=("$oid")
  fi
fi
if [ -z "$branch" ]; then
  case "$spec" in
    "" | *[!0-9]*) branch="${spec:-$(git -C "$ws" branch --show-current 2>/dev/null)}" ;;
    *) branch="$(git -C "$ws" branch --show-current 2>/dev/null)" ;;
  esac
  case "$branch" in */pull/*) branch="$(git -C "$ws" branch --show-current 2>/dev/null)" ;; esac
fi
if [ -n "$branch" ]; then
  tip="$(git -C "$ws" rev-parse --verify -q "refs/heads/$branch^{commit}" 2>/dev/null || true)"
  [ -n "$tip" ] && tips+=("$tip")
fi
[ "${#tips[@]}" -gt 0 ] || exit 1
# Only hex goes into a query.
list=""
for t in "${tips[@]}"; do
  case "$t" in *[!0-9a-f]* | "") continue ;; esac
  list="${list:+$list,}'$t'"
done
[ -n "$list" ] || exit 1

# (1) Open refusals at those tips: for each rule, its latest run at that tip, when it
# holds a failing check or failed as an engine.
# sr-checks finds the session from its working directory, so it runs in the workspace.
refusal_listing=""
if rows="$(cd "$ws" && sr-checks sql "
  select r.check_id as rule, coalesce(c.kind, 'engine') as kind, r.head_ref as tip,
         coalesce(json_extract(c.metadata, '\$.reasoning'), r.error, '') as why
  from check_runs r left join checks c on c.run_id = r.id
  where r.head_ref in ($list)
    and r.id = (select r2.id from check_runs r2 where r2.check_id = r.check_id and r2.head_ref = r.head_ref
                order by r2.run_at desc, r2.rowid desc limit 1)
    and (c.status in ('fail', 'error') or r.exit_code != 0)
  order by r.check_id" 2>/dev/null)"; then
  n="$(printf '%s' "$rows" | jq 'length' 2>/dev/null)" || n=0
  if [ "${n:-0}" -gt 0 ]; then
    refusal_listing="$(printf '%s' "$rows" | jq -r '.[] | "  - \(.rule) (\(.kind)) at \(.tip[0:12]): \(.why | split("\n")[0] | .[0:200])"')"
  fi
fi

# (2) A tip this session recorded (it committed on it) that no rule has PASSED yet: every
# file-guard that ran in this session needs a finished passing run at the tip or above it.
# Merging first lands the work where the Stop that would have judged it can no longer hold
# it (a squash merge even hides the commits), so the work is owed its judgement first.
owed_listing=""
recorded=""
[ -n "${SR_SESSION_ID:-}" ] && recorded="$(cd "$ws" && sr-session refs list --session "$SR_SESSION_ID" 2>/dev/null)"
if [ -n "$recorded" ]; then
  rules="$(cd "$ws" && sr-checks sql "select distinct check_id from check_runs where check_id like '%file-guard/%'" 2>/dev/null | jq -r '.[].check_id' 2>/dev/null)"
  passes="$(cd "$ws" && sr-checks sql "
    select cr.check_id as rule, cr.head_ref as head from check_runs cr
    where cr.check_id like '%file-guard/%' and cr.head_ref <> '' and cr.exit_code = 0 and cr.error is null
      and json_extract(cr.metadata, '\$.state') = 'complete'
      and not exists (select 1 from checks c where c.run_id = cr.id
                      and (c.status in ('fail', 'error', 'interrupted')
                           or json_extract(c.metadata, '\$.staleFrom') is not null))" 2>/dev/null)" || passes="[]"
  for t in "${tips[@]}"; do
    case "$t" in *[!0-9a-f]* | "") continue ;; esac
    # Recorded: a row at this very tip, or one for the branch being merged.
    printf '%s\n' "$recorded" | awk -F'\t' -v t="$t" -v b="refs/heads/$branch" '$3 == t || ($2 == b && b != "refs/heads/") {f = 1} END {exit !f}' || continue
    missing=""
    if [ -z "$rules" ]; then
      missing="  - no file-guard has judged anything in this session yet"
    else
      while IFS= read -r r; do
        [ -n "$r" ] || continue
        ok=""
        while IFS= read -r h; do
          [ -n "$h" ] || continue
          if git -C "$ws" merge-base --is-ancestor "$t" "$h" 2>/dev/null; then ok=1; break; fi
        done < <(printf '%s' "$passes" | jq -r --arg r "$r" '.[]? | select(.rule == $r) | .head' 2>/dev/null)
        [ -n "$ok" ] || missing="${missing:+$missing$'\n'}  - $r"
      done <<<"$rules"
    fi
    [ -n "$missing" ] && owed_listing="${owed_listing:+$owed_listing$'\n'}${t:0:12}, not yet passed by:"$'\n'"$missing"
  done
fi

[ -n "$refusal_listing" ] || [ -n "$owed_listing" ] || exit 1

msg="\`gh pr merge\` is refused: ${branch:-the branch} "
if [ -n "$refusal_listing" ]; then
  msg+="has refusals this session recorded and nobody resolved, judged at its tip:"$'\n'"$refusal_listing"$'\n'
  msg+="Merging past a refusal is never legitimate. Fix what the rule refused (commit the fix and let the Stop judge the new tip), then merge. "
fi
if [ -n "$owed_listing" ]; then
  [ -n "$refusal_listing" ] && msg+=$'\n'"It also has commits this session made that no rule has judged yet: "
  [ -z "$refusal_listing" ] && msg+="holds commits this session made that no rule has judged yet (no finished passing run at its tip): "
  msg+=$'\n'"$owed_listing"$'\n'
  msg+="STOP: end your turn now, without merging, so the Stop hook judges the branch; merge in a later turn once it passed. Merging first lands the work where the rules can no longer hold it. "
  msg+="Judging is the engine's: never run \`sr-session start\` / \`sr-session stop\` or write a judge of your own to get a pass. "
fi
msg+="Only the user can say to merge anyway: cite their exact words, as one command, \`sr-session trajectory cite '<their exact words>' && gh pr merge ...\`."
jq -n --arg m "$msg" '{hint: $m}'
exit 0
