#!/usr/bin/env bash
# `when` of no-merge-over-refusals: exit 0 when the command lands work that is owed a
# refusal, so the user-citation requirement applies; exit 1 when it lands nothing of the
# kind. Prints {"hint": "..."} on stdout: the refusal's own advice.
#
# What lands work, and so is looked at: `gh pr merge` (any flags, --admin included), a
# `gh api` call that merges a pull request, and a `git push` whose destination is the
# default branch. For each, the commits landing are the PR head (by branch name AND by
# the head's oid) or the pushed commit.
#
# It lands work owed a refusal when, anywhere in the session FAMILY (the root session's
# check results and every sub-agent's: `sr-checks sql --family`):
#   - a rule refused one of those commits (or one of the branch's earlier commits not yet
#     in the default branch) and no later pass at a descendant resolved it; or
#   - the session recorded a commit on that branch that some file-guard has not passed
#     yet, so the Stop that would judge it has not run.
#
# It FAILS CLOSED: when it cannot tell what a command lands (a shell variable, loop,
# substitution or glob in the command, a flag it does not know, a pull request gh cannot
# look up), it asks for a user citation as it does for a refusal, and says to name the PR
# literally. Nothing here exits 1 for "could not tell" on a merge.
#
# Known gap: a `git push` whose refspec is built by the shell is not followed; the
# judge-before-push gate is the one that looks at pushes in general.
#
# Contract: stdin is the gate payload (.event.raw, .event.invocations[]).
set -uo pipefail

payload="$(cat)"
ws="${SR_WORKSPACE:-.}"
raw="$(printf '%s' "$payload" | jq -r '.event.raw // ""')"

tips=()        # commits that land
branches=()    # branch names that land
unresolved=()  # why a command could not be followed
what=""        # the command, for the message

add_tip() {
  local t
  t="$(git -C "$ws" rev-parse --verify -q "$1^{commit}" 2>/dev/null || true)"
  [ -n "$t" ] && tips+=("$t")
  return 0
}

# --- the default branch: what already landed is not owed anything -------------------------
defbr=""
for c in "$(git -C "$ws" symbolic-ref -q refs/remotes/origin/HEAD 2>/dev/null || true)" \
  refs/remotes/origin/main refs/remotes/origin/master refs/heads/main refs/heads/master; do
  [ -n "$c" ] || continue
  if git -C "$ws" rev-parse --verify -q "$c^{commit}" >/dev/null 2>&1; then
    defbr="$c"
    break
  fi
done
defnames="main master"
[ -n "$defbr" ] && defnames="$defnames ${defbr##*/}"
in_default() { [ -n "$defbr" ] && git -C "$ws" merge-base --is-ancestor "$1" "$defbr" 2>/dev/null; }

# --- reading the command ----------------------------------------------------------------
# A word the shell has to expand is dropped from argv by the parser, so the line itself is
# what says one was there: a segment that merges (or calls the API) and holds a `$` or a
# backtick outside single quotes cannot be followed.
dynamic_merge_segment() {
  printf '%s' "$raw" | tr ';&|' '\n\n\n' | sed "s/'[^']*'//g" |
    grep -E 'gh[[:space:]].*(pr[[:space:]]+merge|api.*merge)' | grep -q '[$`]'
}

valid_spec() {
  case "$1" in
    "") return 0 ;;
    *[!0-9]*) ;;
    *) return 0 ;;
  esac
  printf '%s' "$1" | grep -Eq '^https?://[^[:space:]]+/pull/[0-9]+([/#?].*)?$' && return 0
  printf '%s' "$1" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._/@+:-]*$' && return 0
  return 1
}

# lookup SPEC REPO: the head of the pull request, by name and by oid.
lookup() {
  local spec="$1" repo="$2" info oid branch
  if ! command -v gh >/dev/null 2>&1; then
    unresolved+=("gh is not available to look up the pull request's head")
    return
  fi
  local a=(pr view)
  [ -n "$spec" ] && a+=("$spec")
  [ -n "$repo" ] && a+=(-R "$repo")
  if ! info="$(cd "$ws" && gh "${a[@]}" --json headRefName,headRefOid 2>/dev/null)"; then
    unresolved+=("gh could not look up the head of pull request ${spec:-of the current branch}")
    return
  fi
  oid="$(printf '%s' "$info" | jq -r '.headRefOid // ""' 2>/dev/null)"
  branch="$(printf '%s' "$info" | jq -r '.headRefName // ""' 2>/dev/null)"
  if [ -z "$oid" ] && [ -z "$branch" ]; then
    unresolved+=("gh returned no head for pull request ${spec:-of the current branch}")
    return
  fi
  [ -n "$oid" ] && tips+=("$oid")
  if [ -n "$branch" ]; then
    branches+=("$branch")
    add_tip "refs/heads/$branch"
    add_tip "refs/remotes/origin/$branch"
  fi
}

# Each handler reads the invocation's argv, one word per line, on stdin.
handle_merge() {
  local argv=() a
  while IFS= read -r a; do argv+=("$a"); done
  what='`gh pr merge`'
  local spec="" repo="" seen="" skip="" bad=""
  for a in "${argv[@]}"; do
    if [ -n "$skip" ]; then
      [ "$skip" = repo ] && repo="$a"
      skip=""
      continue
    fi
    case "$a" in
      merge) seen=1 ;;
      -R | --repo) skip=repo ;;
      -b | --body | -F | --body-file | -t | --subject | -A | --author-email | --match-head-commit) skip=other ;;
      --repo=*) repo="${a#--repo=}" ;;
      --body=* | --body-file=* | --subject=* | --author-email=* | --match-head-commit=*) ;;
      --admin | --auto | --squash | -s | --merge | -m | --rebase | -r | --delete-branch | -d | --disable-auto | -h | --help) ;;
      -*) bad="${bad:+$bad }$a" ;;
      *)
        if [ -z "$seen" ]; then
          :
        elif [ -z "$spec" ]; then
          spec="$a"
        else
          unresolved+=("more than one pull request is named")
        fi
        ;;
    esac
  done
  if [ -n "$bad" ]; then
    unresolved+=("the flag(s) $bad are not known, so whether they take a value, and so which pull request is named, cannot be told")
    return
  fi
  if ! valid_spec "$spec"; then
    unresolved+=("the pull request \"$spec\" is not a number, a URL or a plain branch name")
    return
  fi
  lookup "$spec" "$repo"
}

handle_api() {
  local argv=() a path=""
  while IFS= read -r a; do argv+=("$a"); done
  case "$raw" in *merge*) ;; *) return ;; esac
  what='`gh api` (a pull request merge)'
  for a in "${argv[@]}"; do
    case "$a" in
      *mergePullRequest*)
        unresolved+=("a GraphQL merge mutation names no pull request the gate can look up")
        return
        ;;
    esac
    if printf '%s' "$a" | grep -Eq '^/?repos/[^/]+/[^/]+/pulls/[0-9]+/merge$'; then path="$a"; fi
  done
  if [ -z "$path" ]; then
    unresolved+=("the API call mentions a merge but its path is not a literal repos/<owner>/<repo>/pulls/<number>/merge")
    return
  fi
  path="${path#/}"
  local owner repo num
  IFS=/ read -r _ owner repo _ num _ <<<"$path"
  lookup "$num" "$owner/$repo"
}

handle_push() {
  local argv=() a
  while IFS= read -r a; do argv+=("$a"); done
  local seen="" pos=() skip="" dir="$ws" prev=""
  for a in "${argv[@]}"; do
    if [ "$prev" = "-C" ]; then
      case "$a" in /*) dir="$a" ;; *) dir="$dir/$a" ;; esac
    fi
    prev="$a"
    if [ -z "$seen" ]; then
      [ "$a" = push ] && seen=1
      continue
    fi
    if [ -n "$skip" ]; then
      skip=""
      continue
    fi
    case "$a" in
      -o | --push-option | --repo | --receive-pack | --exec) skip=1 ;;
      -*) ;;
      *) pos+=("$a") ;;
    esac
  done
  local cur specs=() s src dst t
  cur="$(git -C "$dir" branch --show-current 2>/dev/null || true)"
  if [ "${#pos[@]}" -gt 1 ]; then specs=("${pos[@]:1}"); fi
  if [ "${#specs[@]}" -eq 0 ] && [ -n "$cur" ]; then specs=("$cur"); fi
  for s in ${specs[@]+"${specs[@]}"}; do
    s="${s#+}"
    case "$s" in
      *:*) src="${s%%:*}" dst="${s#*:}" ;;
      *) src="$s" dst="$s" ;;
    esac
    dst="${dst#refs/heads/}"
    [ -n "$src" ] || continue # a deletion lands nothing
    case " $defnames " in *" $dst "*) ;; *) continue ;; esac
    what="\`git push\` to $dst"
    t="$(git -C "$dir" rev-parse --verify -q "$src^{commit}" 2>/dev/null || true)"
    if [ -z "$t" ]; then
      unresolved+=("the commit \"$src\" pushed to $dst cannot be resolved")
      continue
    fi
    tips+=("$t")
    case "$src" in
      HEAD) [ -n "$cur" ] && branches+=("$cur") ;;
      refs/heads/*) branches+=("${src#refs/heads/}") ;;
      *) git -C "$dir" show-ref --verify -q "refs/heads/$src" 2>/dev/null && branches+=("$src") ;;
    esac
  done
}

while IFS= read -r inv; do
  [ -n "$inv" ] || continue
  kind="$(printf '%s' "$inv" | jq -r '
    if .bin == "gh" and (.argv | index("pr")) and (.argv | index("merge")) then "merge"
    elif .bin == "gh" and (.argv | index("api")) then "api"
    elif .bin == "git" and (.argv | index("push")) then "push"
    else "" end')"
  case "$kind" in
    merge) handle_merge < <(printf '%s' "$inv" | jq -r '.argv[]') ;;
    api) handle_api < <(printf '%s' "$inv" | jq -r '.argv[]') ;;
    push) handle_push < <(printf '%s' "$inv" | jq -r '.argv[]') ;;
  esac
done < <(printf '%s' "$payload" | jq -c '.event.invocations[]?')

if dynamic_merge_segment; then
  what="${what:-\`gh pr merge\`}"
  unresolved+=("the command line expands a shell variable, substitution or glob where the pull request is named")
fi
[ "${#tips[@]}" -gt 0 ] || [ "${#unresolved[@]}" -gt 0 ] || exit 1

cite_hint="Only the user can say to land it anyway: cite their exact words, as one command, \`sr-session trajectory cite '<their exact words>' && <the command>\`."

# --- fail closed: what lands cannot be told ---------------------------------------------
if [ "${#unresolved[@]}" -gt 0 ]; then
  msg="${what:-This command} is refused: the gate cannot tell which commits it would land, so it cannot tell whether they were refused or are still unjudged:"$'\n'
  for u in "${unresolved[@]}"; do msg+="  - $u"$'\n'; done
  msg+="Name the pull request literally: one \`gh pr merge <number> --squash\` per command, a number, URL or branch, no shell variable, loop, substitution or glob, only the flags \`--admin --auto --squash --merge --rebase --delete-branch --repo --body\`, and make sure \`gh pr view <number>\` works. "
  msg+="$cite_hint"
  jq -n --arg m "$msg" '{hint: $m}'
  exit 0
fi

# Distinct tips and branches.
uniq_words() { printf '%s\n' "$@" | awk 'NF && !s[$0]++'; }
t2=()
while IFS= read -r x; do t2+=("$x"); done < <(uniq_words "${tips[@]}")
tips=("${t2[@]}")
b2=()
while IFS= read -r x; do b2+=("$x"); done < <(uniq_words ${branches[@]+"${branches[@]}"})
branches=(${b2[@]+"${b2[@]}"})

# related H: H is one of the landing commits, or an earlier commit of the same line of
# work that the default branch does not hold yet.
related() {
  local h="$1" t
  for t in "${tips[@]}"; do
    [ "$h" = "$t" ] && return 0
  done
  in_default "$h" && return 1
  for t in "${tips[@]}"; do
    git -C "$ws" merge-base --is-ancestor "$h" "$t" 2>/dev/null && return 0
  done
  return 1
}

# --- (1) refusals: every store of the session family -----------------------------------
# Each rule's latest run per judged head, per store: bad = a failing check or an engine
# failure; good = a finished, passing run.
refusal_listing=""
runs="$(cd "$ws" && sr-checks sql --family "
  select r.check_id as rule, r.head_ref as head, r.run_at as run_at,
         case when r.exit_code != 0 or exists (select 1 from checks c where c.run_id = r.id and c.status in ('fail', 'error'))
              then 1 else 0 end as bad,
         case when r.exit_code = 0 and r.error is null and json_extract(r.metadata, '\$.state') = 'complete'
                   and not exists (select 1 from checks c where c.run_id = r.id
                                   and (c.status in ('fail', 'error', 'interrupted')
                                        or json_extract(c.metadata, '\$.staleFrom') is not null))
              then 1 else 0 end as good,
         coalesce((select c.kind from checks c where c.run_id = r.id and c.status in ('fail', 'error') limit 1), 'engine') as kind,
         coalesce((select json_extract(c.metadata, '\$.reasoning') from checks c where c.run_id = r.id and c.status in ('fail', 'error') limit 1),
                  r.error, '') as why
  from check_runs r
  where r.head_ref <> ''
    and r.id = (select r2.id from check_runs r2 where r2.check_id = r.check_id and r2.head_ref = r.head_ref
                order by r2.run_at desc, r2.rowid desc limit 1)" 2>&1)"
if ! printf '%s' "$runs" | jq -e 'type == "array"' >/dev/null 2>&1; then
  case "$runs" in
    *"no session to read"*) runs="[]" ;; # not in a session: nothing was ever recorded
    *)
      jq -n --arg m "${what:-This command} is refused: the check results of this session and its sub-agents could not be read (${runs:0:200}), so the gate cannot tell whether what lands was refused. $cite_hint" '{hint: $m}'
      exit 0
      ;;
  esac
fi

heads_bad="$(printf '%s' "$runs" | jq -r '.[] | select(.bad == 1) | .head' | sort -u)"
while IFS= read -r h; do
  [ -n "$h" ] || continue
  case "$h" in *[!0-9a-f]*) continue ;; esac
  related "$h" || continue
  # Each rule that refused at H, unless a later finished pass at H or a descendant fixed it.
  while IFS=$'\t' read -r rule kind at why; do
    [ -n "$rule" ] || continue
    fixed=""
    while IFS= read -r g; do
      [ -n "$g" ] || continue
      if git -C "$ws" merge-base --is-ancestor "$h" "$g" 2>/dev/null; then fixed=1; break; fi
    done < <(printf '%s' "$runs" | jq -r --arg r "$rule" --arg at "$at" '.[] | select(.rule == $r and .good == 1 and .bad == 0 and (.run_at | tostring) > $at) | .head')
    [ -n "$fixed" ] && continue
    refusal_listing="${refusal_listing:+$refusal_listing$'\n'}  - $rule ($kind) at ${h:0:12}: $(printf '%s' "$why" | head -n1 | cut -c1-200)"
  done < <(printf '%s' "$runs" | jq -r --arg h "$h" '.[] | select(.bad == 1 and .head == $h) | [.rule, .kind, (.run_at | tostring), .why] | @tsv')
done <<<"$heads_bad"

# --- (2) a commit this session recorded that no rule has passed yet --------------------
# Every file-guard that ran anywhere in the family needs a finished passing run at the
# commit or above it. The recorded commits are the root session's registry, which holds the
# ones its sub-agents made too.
owed_listing=""
recorded=""
[ -n "${SR_SESSION_ID:-}" ] && recorded="$(cd "$ws" && sr-session refs list --session "$SR_SESSION_ID" 2>/dev/null)"
if [ -n "$recorded" ]; then
  rules="$(printf '%s' "$runs" | jq -r '.[] | select(.rule | contains("file-guard/")) | .rule' | sort -u)"
  for t in "${tips[@]}"; do
    case "$t" in *[!0-9a-f]* | "") continue ;; esac
    hit=""
    while IFS=$'\t' read -r _ rname rtip _; do
      [ -n "$rtip" ] || continue
      if [ "$rtip" = "$t" ]; then hit=1; break; fi
      for b in ${branches[@]+"${branches[@]}"}; do
        [ -n "$b" ] && [ "$rname" = "refs/heads/$b" ] && hit=1
      done
      [ -n "$hit" ] && break
      case "$rtip" in *[!0-9a-f]*) continue ;; esac
      if ! in_default "$rtip" && git -C "$ws" merge-base --is-ancestor "$rtip" "$t" 2>/dev/null; then hit=1; break; fi
    done <<<"$recorded"
    [ -n "$hit" ] || continue
    missing=""
    if [ -z "$rules" ]; then
      missing="  - no file-guard has judged anything in this session yet"
    else
      while IFS= read -r r; do
        [ -n "$r" ] || continue
        ok=""
        while IFS= read -r g; do
          [ -n "$g" ] || continue
          if git -C "$ws" merge-base --is-ancestor "$t" "$g" 2>/dev/null; then ok=1; break; fi
        done < <(printf '%s' "$runs" | jq -r --arg r "$r" '.[] | select(.rule == $r and .good == 1) | .head')
        [ -n "$ok" ] || missing="${missing:+$missing$'\n'}  - $r"
      done <<<"$rules"
    fi
    [ -n "$missing" ] && owed_listing="${owed_listing:+$owed_listing$'\n'}${t:0:12}, not yet passed by:"$'\n'"$missing"
  done
fi

[ -n "$refusal_listing" ] || [ -n "$owed_listing" ] || exit 1

branch_label="${branches[0]:-the commits}"
msg="${what:-\`gh pr merge\`} is refused: $branch_label "
if [ -n "$refusal_listing" ]; then
  msg+="has refusals this session (or one of its sub-agents) recorded and nobody resolved:"$'\n'"$refusal_listing"$'\n'
  msg+="Landing past a refusal is never legitimate. Fix what the rule refused (commit the fix and let the Stop judge the new tip), then merge. "
fi
if [ -n "$owed_listing" ]; then
  [ -n "$refusal_listing" ] && msg+=$'\n'"It also has commits this session made that no rule has judged yet: "
  [ -z "$refusal_listing" ] && msg+="holds commits this session made that no rule has judged yet (no finished passing run at its tip): "
  msg+=$'\n'"$owed_listing"$'\n'
  msg+="STOP: end your turn now, without merging, so the Stop hook judges the branch (a sub-agent's work is judged when it finishes); merge in a later turn once it passed. Merging first lands the work where the rules can no longer hold it. "
  msg+="Judging is the engine's: never run \`sr-session start\` / \`sr-session stop\` or write a judge of your own to get a pass. "
fi
msg+="$cite_hint"
jq -n --arg m "$msg" '{hint: $m}'
exit 0
