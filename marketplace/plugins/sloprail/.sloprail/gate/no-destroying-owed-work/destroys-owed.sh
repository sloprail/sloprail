#!/usr/bin/env bash
# The check of no-destroying-owed-work: exit 1 with {"reason": "..."} when the command would
# destroy commits that are owed a judgement or a fix; exit 0 otherwise. Facts only: no user
# citation lifts it. What "owed" means is the merge gate's (owed-work.sh).
#
# FAIL CLOSED where it matters: a target the script cannot determine is refused when
# anything at all is owed, and passes when nothing is.
#
# Contract: stdin is the gate payload (.event.raw, .event.invocations[]).
set -uo pipefail

payload="$(cat)"
ws="${SR_WORKSPACE:-.}"
raw="$(printf '%s' "$payload" | jq -r '.event.raw // ""')"

lib_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
unset owed_work_loaded
. "$lib_dir/owed-work.sh" || owed_work_loaded=""
if [ "${owed_work_loaded:-}" != 1 ]; then
  # A partly loaded helper decides nothing: refuse (exit 0 would wave it through).
  jq -n '{reason: "the gate'"'"'s own helper (owed-work.sh) did not load, so the gate cannot tell what this command destroys. Reinstall the sloprail plugin or fix the helper; until then end your turn so the Stop judges the work, and name the target literally."}'
  exit 1
fi
owed_setup

tips=()        # commits that would be destroyed
branches=()    # branch names that would be destroyed
dirs=()        # where they live
unresolved=()  # why a command could not be followed
global=""      # a command that endangers everything at once
what=""        # the command, for the message

add_ref() { # DIR REF...: the commit each ref holds, when it exists
  local dir="$1" r t
  shift
  for r in "$@"; do
    t="$(git -C "$dir" rev-parse --verify -q "$r^{commit}" 2>/dev/null || true)"
    [ -n "$t" ] && tips+=("$t") && dirs+=("$dir")
  done
  return 0
}

# A word the shell has to expand is dropped from argv by the parser, so the line itself says
# one was there: a git segment of the kinds watched that holds a `$` or backtick outside
# single quotes cannot be followed.
dynamic_git_segment() {
  printf '%s' "$raw" | tr ';&|' '\n\n\n' | sed "s/'[^']*'//g" |
    grep -E 'git[[:space:]].*(branch|push|update-ref|worktree|reset|checkout|switch|gc|prune|reflog)' | grep -q '[$`]'
}

handle_git() {
  local cwd="$1" argv=() a
  while IFS= read -r a; do argv+=("$a"); done

  # Where it runs.
  # The invocation's cwd is the command parser's own tracking of `cd` (relative to the
  # workspace until a `cd` goes absolute; "" when a `cd` could not be followed, e.g. `cd ~/x`
  # or `cd "$VAR"`). An unknown directory only matters to what is named relative to it: an
  # absolute `-C`, or an absolute worktree path, does not need it.
  local dir="$ws" dirunknown=""
  case "$cwd" in
    ".") ;;
    "") dirunknown=1 ;;
    /*) dir="$cwd" ;;
    *) dir="$ws/$cwd" ;;
  esac
  # Global options, then the subcommand and its arguments.
  local i=1 n="${#argv[@]}" sub="" args=()
  while [ "$i" -lt "$n" ]; do
    a="${argv[$i]}"
    case "$a" in
      -C)
        i=$((i + 1))
        case "${argv[$i]:-}" in /*) dir="${argv[$i]}" dirunknown="" ;; *) dir="$dir/${argv[$i]:-}" ;; esac
        ;;
      -c | --exec-path | --namespace) i=$((i + 1)) ;;
      -*) ;;
      *)
        sub="$a"
        args=("${argv[@]:$((i + 1))}")
        break
        ;;
    esac
    i=$((i + 1))
  done
  [ -n "$sub" ] || return 0
  if [ -n "$dirunknown" ]; then
    # Only these do not depend on where the command runs.
    local indep=""
    case "$sub" in gc | prune | reflog) indep=1 ;; esac
    if [ "$sub" = worktree ] && [ "${args[0]:-}" = remove ]; then
      for a in "${args[@]:1}"; do
        case "$a" in /*) indep=1 ;; esac
      done
    fi
    if [ -z "$indep" ]; then
      unresolved+=("the directory a \`cd\` moved the command to cannot be told (spell the path absolute: \`git -C /abs/dir ...\`, \`git worktree remove /abs/path\`)")
      what='`git '"$sub"'`'
      return 0
    fi
  fi
  local cur
  cur="$(git -C "$dir" branch --show-current 2>/dev/null || true)"

  local x y flags positional delete="" force="" remote="" dashdash=""
  positional=()
  case "$sub" in
    branch)
      what='`git branch`'
      local del="" frc="" rem=""
      for x in ${args[@]+"${args[@]}"}; do
        case "$x" in
          --delete) del=1 ;;
          --force) frc=1 ;;
          --remotes) rem=1 ;;
          --*) ;;
          -*)
            case "$x" in *d* | *D*) del=1 ;; esac
            case "$x" in *f*) frc=1 ;; esac
            case "$x" in *r*) rem=1 ;; esac
            ;;
          *) positional+=("$x") ;;
        esac
      done
      if [ -n "$del" ]; then
        what='`git branch -d/-D`'
        for x in ${positional[@]+"${positional[@]}"}; do
          if [ -n "$rem" ]; then add_ref "$dir" "refs/remotes/$x"; else add_ref "$dir" "refs/heads/$x"; branches+=("$x"); fi
        done
      elif [ -n "$frc" ] && [ "${#positional[@]}" -ge 1 ]; then
        what='`git branch -f` over an existing branch'
        add_ref "$dir" "refs/heads/${positional[0]}"
        branches+=("${positional[0]}")
      fi
      ;;
    checkout | switch)
      local nb=""
      y=""
      for x in ${args[@]+"${args[@]}"}; do
        if [ -n "$y" ]; then
          nb="$x"
          y=""
          continue
        fi
        case "$x" in
          -B | -C | --force-create) y=1 ;;
          -B* | -C*) nb="${x#-?}" ;;
          --force-create=*) nb="${x#--force-create=}" ;;
        esac
      done
      if [ -n "$nb" ]; then
        what="\`git $sub\` over an existing branch"
        add_ref "$dir" "refs/heads/$nb"
        branches+=("$nb")
      fi
      ;;
    push)
      what='`git push --delete`'
      local pos=() del=""
      skip=""
      for x in ${args[@]+"${args[@]}"}; do
        if [ -n "$skip" ]; then skip=""; continue; fi
        case "$x" in
          -o | --push-option | --repo | --receive-pack | --exec) skip=1 ;;
          --delete | -d) del=1 ;;
          -*) ;;
          *) pos+=("$x") ;;
        esac
      done
      remote="${pos[0]:-origin}"
      local refs=()
      if [ "${#pos[@]}" -gt 1 ]; then refs=("${pos[@]:1}"); fi
      for x in ${refs[@]+"${refs[@]}"}; do
        local b=""
        if [ -n "$del" ]; then
          b="$x"
        else
          case "$x" in :*) b="${x#:}" ;; esac
        fi
        [ -n "$b" ] || continue
        b="${b#refs/heads/}"
        add_ref "$dir" "refs/heads/$b" "refs/remotes/$remote/$b"
        branches+=("$b")
      done
      ;;
    update-ref)
      what='`git update-ref -d`'
      local isdel="" ref="" skip=""
      for x in ${args[@]+"${args[@]}"}; do
        if [ -n "$skip" ]; then skip=""; continue; fi
        case "$x" in
          -d) isdel=1 ;;
          -m) skip=1 ;;
          --stdin) unresolved+=("\`update-ref --stdin\` names its refs on stdin"); ;;
          -*) ;;
          *) [ -z "$ref" ] && ref="$x" ;;
        esac
      done
      if [ -n "$isdel" ] && [ -n "$ref" ]; then
        add_ref "$dir" "$ref"
        case "$ref" in refs/heads/*) branches+=("${ref#refs/heads/}") ;; esac
      fi
      ;;
    worktree)
      what='`git worktree remove`'
      if [ "${#args[@]}" -gt 0 ] && [ "${args[0]}" = remove ]; then
        local path=""
        for x in "${args[@]:1}"; do
          case "$x" in -*) ;; *) path="$x" ;; esac
        done
        if [ -z "$path" ]; then
          unresolved+=("the worktree to remove is not named")
        else
          case "$path" in /*) ;; *) path="$dir/$path" ;; esac
          local wt wb
          wt="$(git -C "$path" rev-parse --verify -q 'HEAD^{commit}' 2>/dev/null || true)"
          if [ -z "$wt" ]; then
            unresolved+=("the worktree $path cannot be read, so what it holds is unknown")
          else
            tips+=("$wt")
            dirs+=("$path")
            wb="$(git -C "$path" branch --show-current 2>/dev/null || true)"
            [ -n "$wb" ] && branches+=("$wb")
          fi
        fi
      fi
      ;;
    reset)
      what='`git reset`'
      # --soft and --mixed keep what the commits changed in the index or the tree: the
      # squash the engine's own refusals advise (`reset --soft`, then commit again) loses
      # nothing, and the new commit is judged at Stop. Only --hard, --keep and --merge
      # drop the content.
      local rev="" hard=""
      for x in ${args[@]+"${args[@]}"}; do
        case "$x" in
          --) break ;;
          --hard | --keep | --merge) hard=1 ;;
          -*) ;;
          *) [ -z "$rev" ] && rev="$x" ;;
        esac
      done
      if [ -n "$rev" ] && [ -n "$hard" ]; then
        local head target
        head="$(git -C "$dir" rev-parse --verify -q 'HEAD^{commit}' 2>/dev/null || true)"
        target="$(git -C "$dir" rev-parse --verify -q "$rev^{commit}" 2>/dev/null || true)"
        # A path (not a commit) resets the index only: nothing is destroyed.
        if [ -n "$head" ] && [ -n "$target" ] && ! git -C "$dir" merge-base --is-ancestor "$head" "$target" 2>/dev/null; then
          tips+=("$head")
          dirs+=("$dir")
          [ -n "$cur" ] && branches+=("$cur")
        fi
      fi
      ;;
    gc)
      for x in ${args[@]+"${args[@]}"}; do
        case "$x" in
          --prune | --prune=now | --prune=all) global=1; what='`git gc --prune`' ;;
        esac
      done
      ;;
    prune)
      global=1
      what='`git prune`'
      ;;
    reflog)
      if [ "${#args[@]}" -gt 0 ]; then
        case "${args[0]}" in expire | delete) global=1; what="\`git reflog ${args[0]}\`" ;; esac
      fi
      ;;
  esac
  return 0
}

while IFS= read -r inv; do
  [ -n "$inv" ] || continue
  cwd="$(printf '%s' "$inv" | jq -r '.cwd // "."')"
  handle_git "$cwd" < <(printf '%s' "$inv" | jq -r 'select(.bin == "git") | .argv[]')
done < <(printf '%s' "$payload" | jq -c '.event.invocations[]? | select(.bin == "git")')

if dynamic_git_segment; then
  unresolved+=("the command line expands a shell variable, substitution or glob where a ref or path is named")
fi

[ "${#tips[@]}" -gt 0 ] || [ "${#unresolved[@]}" -gt 0 ] || [ -n "$global" ] || exit 0

if ! owed_load; then
  jq -n --arg m "${what:-This command} is refused: the check results of this session and its sub-agents could not be read ($owed_error), so the gate cannot tell whether it destroys work that was never judged. Fix the store (\`sr-checks sql --family 'select 1'\` shows the error) and retry." '{reason: $m}'
  exit 1
fi

if [ "${#unresolved[@]}" -gt 0 ] || [ -n "$global" ]; then
  # Everything at stake at once: refuse only if anything at all is owed.
  owed_all
else
  owed_unique
fi
owed_evaluate
[ -n "$refusal_listing" ] || [ -n "$owed_listing" ] || exit 0

msg="${what:-This command} is refused: it would destroy "
if [ "${#unresolved[@]}" -gt 0 ]; then
  msg+="work this session cannot be shown not to owe: the gate cannot tell what it destroys ("
  for u in "${unresolved[@]}"; do msg+="$u; "; done
  msg="${msg%; })"
elif [ -n "$global" ]; then
  msg+="every unreachable commit at once, and some of this session's are still owed"
else
  if [ "${#branches[@]}" -gt 0 ]; then msg+="the commits of branch ${branches[0]} that are still owed"; else msg+="commits that are still owed"; fi
fi
msg+=":"$'\n'
[ -n "$refusal_listing" ] && msg+="refused and not fixed:"$'\n'"$refusal_listing"$'\n'
[ -n "$owed_listing" ] && msg+="not judged yet:"$'\n'"$owed_listing"$'\n'
msg+="Fix the work or end your turn now, without running it, so the Stop hook judges it (a sub-agent's work is judged when it finishes); delete in a later turn once it passed, or name the exact branch or path literally. "
msg+="No user citation lifts this refusal. If the USER wants the work dropped, ask them: only \`sr-session refs abandon --ref <branch> --folder <dir> --cite-user '<their exact words>'\` drops a branch from what is owed. "
jq -n --arg m "$msg" '{reason: $m}'
exit 1
