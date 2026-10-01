#!/usr/bin/env bash
# `when` of no-merge-over-refusals: exit 0 when the branch a `gh pr merge` would
# merge has open refusals at its tip (so the user-citation requirement applies),
# exit 1 when it has none or the branch cannot be resolved (nothing to refuse on).
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

# Open refusals at those tips: for each rule, its latest run at that tip, when it
# holds a failing check or failed as an engine. Only hex goes into the query.
list=""
for t in "${tips[@]}"; do
  case "$t" in *[!0-9a-f]* | "") continue ;; esac
  list="${list:+$list,}'$t'"
done
[ -n "$list" ] || exit 1
# sr-checks finds the session from its working directory, so it runs in the workspace.
rows="$(cd "$ws" && sr-checks sql "
  select r.check_id as rule, coalesce(c.kind, 'engine') as kind, r.head_ref as tip,
         coalesce(json_extract(c.metadata, '\$.reasoning'), r.error, '') as why
  from check_runs r left join checks c on c.run_id = r.id
  where r.head_ref in ($list)
    and r.id = (select r2.id from check_runs r2 where r2.check_id = r.check_id and r2.head_ref = r.head_ref
                order by r2.run_at desc, r2.rowid desc limit 1)
    and (c.status in ('fail', 'error') or r.exit_code != 0)
  order by r.check_id" 2>/dev/null)" || exit 1
n="$(printf '%s' "$rows" | jq 'length' 2>/dev/null)" || exit 1
[ "${n:-0}" -gt 0 ] || exit 1

listing="$(printf '%s' "$rows" | jq -r '.[] | "  - \(.rule) (\(.kind)) at \(.tip[0:12]): \(.why | split("\n")[0] | .[0:200])"')"
jq -n --arg b "${branch:-the branch}" --arg l "$listing" '{hint:
  ("`gh pr merge` is refused: " + $b + " has refusals this session recorded and nobody resolved, judged at its tip:\n" + $l +
   "\nMerging past a refusal is never legitimate. Fix what the rule refused (commit the fix and let the Stop judge the new tip), then merge. " +
   "Only the user can say to merge anyway: cite their exact words, as one command, `sr-session trajectory cite '"'"'<their exact words>'"'"' && gh pr merge ...`.")}'
exit 0
