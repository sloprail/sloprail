#!/usr/bin/env bash
# The check of no-merge-over-refusals: exit 1 with {"reason": "..."} when the command lands
# work that is owed (a refusal nobody fixed, or commits no rule has judged), or lands what
# it cannot tell; exit 0 when it lands nothing of the kind. Facts only: no user citation
# lifts it.
#
# What lands work, and so is looked at: `gh pr merge` (any flags, --admin included), a
# `gh api` call that merges a pull request, and a `git push` whose destination is the
# default branch. For each, the commits landing are the PR head (by branch name AND by
# the head's oid) or the pushed commit. What "owed" means is owed-work.sh's (sourced).
#
# It FAILS CLOSED: when it cannot tell what a command lands (a shell variable, loop,
# substitution or glob in the command, a flag it does not know, a pull request gh cannot
# look up), it refuses as it does for a refusal, and says to name the PR literally.
# Nothing here exits 0 for "could not tell" on a merge.
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

lib_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
unset owed_work_loaded
. "$lib_dir/owed-work.sh" || owed_work_loaded=""
if [ "${owed_work_loaded:-}" != 1 ]; then
  # A partly loaded helper decides nothing: refuse (exit 0 would wave it through).
  jq -n '{reason: "the gate'"'"'s own helper (owed-work.sh) did not load, so the gate cannot tell what this command lands. Reinstall the sloprail plugin or fix the helper; until then name the pull request literally and let the Stop judge the branch first."}'
  exit 1
fi
owed_setup

add_tip() {
  local t
  t="$(git -C "$ws" rev-parse --verify -q "$1^{commit}" 2>/dev/null || true)"
  [ -n "$t" ] && tips+=("$t")
  return 0
}

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
[ "${#tips[@]}" -gt 0 ] || [ "${#unresolved[@]}" -gt 0 ] || exit 0

# --- fail closed: what lands cannot be told ---------------------------------------------
if [ "${#unresolved[@]}" -gt 0 ]; then
  msg="${what:-This command} is refused: the gate cannot tell which commits it would land, so it cannot tell whether they were refused or are still unjudged:"$'\n'
  for u in "${unresolved[@]}"; do msg+="  - $u"$'\n'; done
  msg+="Name the pull request literally: one \`gh pr merge <number> --squash\` per command, a number, URL or branch, no shell variable, loop, substitution or glob, only the flags \`--admin --auto --squash --merge --rebase --delete-branch --repo --body\`, and make sure \`gh pr view <number>\` works. "
  jq -n --arg m "$msg" '{reason: $m}'
  exit 1
fi

owed_unique

# --- what is owed: every store of the session family -----------------------------------
if ! owed_load; then
  jq -n --arg m "${what:-This command} is refused: the check results of this session and its sub-agents could not be read ($owed_error), so the gate cannot tell whether what lands was refused. Fix the store (\`sr-checks sql --family 'select 1'\` shows the error) and retry." '{reason: $m}'
  exit 1
fi
owed_evaluate

[ -n "$refusal_listing" ] || [ -n "$owed_listing" ] || exit 0

branch_label="${branches[0]:-the commits}"
msg="${what:-\`gh pr merge\`} is refused: $branch_label "
if [ -n "$refusal_listing" ]; then
  msg+="has refusals this session (or one of its sub-agents) recorded and nobody resolved:"$'\n'"$refusal_listing"$'\n'
  msg+="Landing past a refusal is never legitimate, whoever asks. Fix what the rule refused (commit the fix and let the Stop judge the new tip), then merge. "
fi
if [ -n "$owed_listing" ]; then
  [ -n "$refusal_listing" ] && msg+=$'\n'"It also has commits this session made that no rule has judged yet: "
  [ -z "$refusal_listing" ] && msg+="holds commits this session made that no rule has judged yet (no finished passing run at its tip): "
  msg+=$'\n'"$owed_listing"$'\n'
  msg+="STOP: end your turn now, without merging, so the Stop hook judges the branch (a sub-agent's work is judged when it finishes); merge in a later turn once it passed. Merging first lands the work where the rules can no longer hold it. "
  msg+="Judging is the engine's: never run \`sr-session start\` / \`sr-session stop\` or write a judge of your own to get a pass. "
fi
jq -n --arg m "$msg" '{reason: $m}'
exit 1
