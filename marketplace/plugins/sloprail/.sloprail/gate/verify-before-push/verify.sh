#!/usr/bin/env bash
# The gate's match found a `git ... push` among the line's invocations. Refuse it unless every
# ref it would update passes `sr-checks verify` over merge-base(default branch, sha)..sha.
# Contract: stdin is the GateCheckPayload; exit 1 with {"reason": ...} refuses.
# Fails closed: a push whose refs, folder or default branch cannot be resolved is refused.
set -uo pipefail

payload="$(cat)"
refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset gitargs_loaded
. "$lib_dir/gitargs.sh" || refuse "verify-before-push could not load its helpers, so this push could not be checked"
[ "${gitargs_loaded:-}" = 1 ] || refuse "verify-before-push's helpers loaded only partly, so this push could not be checked"

command -v sr-checks >/dev/null 2>&1 ||
  refuse "sr-checks is not on PATH, so the commits this push would send could not be verified. Install the sloprail plugin's binaries, then push again."

n="$(printf '%s' "$payload" | jq -r '.event.invocations | length')" || n=""
case "$n" in '' | *[!0-9]*) refuse "the command's invocations could not be read, so this push could not be checked" ;; esac

# The gate runs BEFORE the line: a push chained after a command that moves refs would be judged
# against the commits as they stand now, not as they will be when it runs.
movers=" commit merge rebase reset checkout switch cherry-pick pull am revert tag branch fetch update-ref symbolic-ref "
pushes=()
mover=""
i=0
while [ "$i" -lt "$n" ]; do
  inv="$(printf '%s' "$payload" | jq -c --argjson i "$i" '.event.invocations[$i]')" ||
    refuse "invocation $i of the command could not be read, so this push could not be checked"
  i=$((i + 1))
  [ "$(printf '%s' "$inv" | jq -r '.bin // ""')" = "git" ] || continue
  git_split "$inv"
  if [ "$SUB" = "push" ]; then
    pushes+=("$inv")
  elif [ -n "$SUB" ] && [[ "$movers" == *" $SUB "* ]]; then
    mover="$SUB"
  fi
done
[ "${#pushes[@]}" -gt 0 ] || exit 0
if [ -n "$mover" ]; then
  refuse "Run the push as its own command: this line runs 'git $mover' and a 'git push' together, and the push is checked before the line runs, so it would be judged against commits that are about to change. Run 'git $mover' first, then 'sr-checks run --base <base> --head HEAD' if needed, then the push as its own command."
fi

# root_commit SHA — the widest base `verify --base` accepts: the first root commit of SHA's history.
root_commit() {
  git "${GOPTS[@]+"${GOPTS[@]}"}" rev-list --max-parents=0 --reverse --date-order "$1" 2>/dev/null | head -n 1
}

# default_base SHA — where work on SHA started, from `sr-checks default-base` (the one
# implementation, gitrepo.DefaultBase). A repository with no remote default branch (a first push to
# a new remote, no origin/HEAD) has none to ask: the whole history is judged from the root commit.
# The base is never taken from the session's refs registry (`sr-session refs track --base` is
# agent-callable, so it could narrow what the push gate verifies). Never an empty range, and
# never a push that cannot be pushed.
default_base() {
  local sha="$1" b
  if b="$(sr-checks default-base --head "$sha" 2>/dev/null)"; then
    if [ "$b" = "4b825dc642cb6eb9a060e54bf8d69288fbee4904" ]; then
      root_commit "$sha"
      return
    fi
    printf '%s' "$b"
    return
  fi
  root_commit "$sha"
}

# results_pending — succeeds when this folder's local results branch holds commits the remote does
# not (a `sr-checks run` whose push failed). verify and CI read the remote's copy, so a push that
# leaves them behind would read as "not judged". The tracking ref is the remote as last fetched or pushed.
results_pending() {
  local ref=refs/sloprail/checks
  git "${GOPTS[@]+"${GOPTS[@]}"}" rev-parse -q --verify "$ref^{commit}" >/dev/null 2>&1 || return 1
  git "${GOPTS[@]+"${GOPTS[@]}"}" remote get-url origin >/dev/null 2>&1 || return 1
  git "${GOPTS[@]+"${GOPTS[@]}"}" rev-parse -q --verify "$ref-remote^{commit}" >/dev/null 2>&1 || return 0
  # a failing rev-list is read as "pending": never as "nothing to push"
  local ahead
  ahead="$(git "${GOPTS[@]+"${GOPTS[@]}"}" rev-list -n 1 "$ref-remote..$ref" 2>/dev/null)" || return 0
  [ -n "$ahead" ]
}

# push_error — why `sr-checks run`'s last push of the results failed, as it recorded it.
push_error() {
  local common
  common="$(git "${GOPTS[@]+"${GOPTS[@]}"}" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)" || return 0
  head -c 600 "$common/sloprail-checks-push-error" 2>/dev/null || true
}

# drop_unsafe_gopts — removes the global options that make git run agent-chosen code (-c,
# --config-env, --exec-path) from GOPTS, which every git call below passes on.
drop_unsafe_gopts() {
  local kept=() i=0 n=${#GOPTS[@]} a
  while [ "$i" -lt "$n" ]; do
    a="${GOPTS[$i]}"
    case "$a" in
      -c | --config-env) i=$((i + 1)) ;;
      -c* | --config-env=* | --exec-path | --exec-path=*) ;;
      *) kept+=("$a") ;;
    esac
    i=$((i + 1))
  done
  GOPTS=(${kept[@]+"${kept[@]}"})
}

# resolve_push_pairs DIR — from REST (the push's arguments) and DIR's local refs only, sets
# PUSH_PAIRS to one "<flag>\t<from>:<to>" line per ref the push would update (flag ' ': update,
# '-': delete), and PUSH_WHY on failure. Reads no remote and runs none of the agent's options.
resolve_push_pairs() {
  local dir="$1" all="" mirror="" tags="" repo="" ddash="" a skip=""
  local specs=() pos=() spec src dst r pre
  PUSH_PAIRS="" PUSH_WHY=""
  for a in ${REST[@]+"${REST[@]}"}; do
    if [ -n "$skip" ]; then skip=""; continue; fi
    if [ -z "$ddash" ]; then
      case "$a" in
        --) ddash=1; continue ;;
        --all | --branches) all=1; continue ;;
        --mirror) mirror=1; continue ;;
        --tags) tags=1; continue ;;
        --repo | --receive-pack | --exec | -o | --push-option) skip=1; [ "$a" = "--repo" ] && repo=1; continue ;;
        -*) continue ;;
      esac
    fi
    pos+=("$a")
  done
  if [ -n "$repo" ]; then specs=(${pos[@]+"${pos[@]}"}); else specs=(${pos[@]+"${pos[@]:1}"}); fi
  local g=(git ${GOPTS[@]+"${GOPTS[@]}"})
  if [ -n "$all" ] || [ -n "$mirror" ]; then
    pre="refs/heads/"
    [ -z "$mirror" ] || pre="refs/"
    while IFS= read -r r; do
      [ -n "$r" ] && specs+=("$r:$r")
    done < <(cd "$dir" && "${g[@]}" for-each-ref --format='%(refname)' "$pre" 2>/dev/null)
  fi
  if [ -n "$tags" ]; then
    while IFS= read -r r; do
      [ -n "$r" ] && specs+=("$r:$r")
    done < <(cd "$dir" && "${g[@]}" for-each-ref --format='%(refname)' refs/tags/ 2>/dev/null)
  fi
  if [ "${#specs[@]}" -eq 0 ]; then
    # no refspec: the current branch, to its same-named branch
    r="$(cd "$dir" && "${g[@]}" symbolic-ref -q HEAD 2>/dev/null)" || {
      PUSH_WHY="HEAD is detached and the push names no refspec"
      return 1
    }
    specs=("$r:$r")
  fi
  for spec in "${specs[@]}"; do
    spec="${spec#+}"
    case "$spec" in
      *:*) src="${spec%%:*}"; dst="${spec#*:}" ;;
      *) src="$spec"; dst="$spec" ;;
    esac
    if [ -z "$src" ]; then PUSH_PAIRS+="-"$'\t'":$dst"$'\n'; continue; fi # a delete sends no commits
    [ -n "$dst" ] || dst="$src"
    if [[ "$src" == *'*'* ]]; then
      local m cand frag
      while IFS= read -r r; do
        [ -n "$r" ] || continue
        m=""
        for cand in "$r" "${r#refs/heads/}" "${r#refs/tags/}"; do
          # shellcheck disable=SC2053
          if [[ "$cand" == $src ]]; then m="$cand"; break; fi
        done
        [ -n "$m" ] || continue
        frag="${m#"${src%%\**}"}"
        frag="${frag%"${src#*\*}"}"
        PUSH_PAIRS+=" "$'\t'"$r:${dst/\*/$frag}"$'\n'
      done < <(cd "$dir" && "${g[@]}" for-each-ref --format='%(refname)' 2>/dev/null)
      continue
    fi
    PUSH_PAIRS+=" "$'\t'"$src:$dst"$'\n'
  done
  return 0
}

for inv in "${pushes[@]}"; do
  git_split "$inv"
  # git's own help pages push nothing.
  skip=""
  for a in ${REST[@]+"${REST[@]}"}; do case "$a" in -h | --help) skip=1 ;; esac; done
  [ -z "$skip" ] || continue

  cwd="$(printf '%s' "$inv" | jq -r '.cwd // ""')"
  [ -n "$cwd" ] || refuse "The folder this push runs in could not be told from the command line (a cd to a variable, an eval), so the commits it would send could not be checked. Run it as 'git -C <dir> push ...' with a literal folder."
  case "$cwd" in /*) dir="$cwd" ;; *) dir="${SR_WORKSPACE:-.}/$cwd" ;; esac
  [ -d "$dir" ] || refuse "The folder this push runs in ($dir) does not exist, so the commits it would send could not be checked."

  # The pushed refs are worked out HERE, from the command line and local refs only. The gate never
  # runs the agent's own git options or contacts the remote: `-c core.sshCommand=...`, `ext::` URLs
  # and `--receive-pack` would run agent-chosen code (or hang) inside the gate.
  drop_unsafe_gopts
  resolve_push_pairs "$dir" || refuse "This push could not be resolved to the refs it would update in $dir, so what leaves could not be checked: $PUSH_WHY"

  while IFS=$'\t' read -r flag pair _; do
    case "$flag" in ' ' | '+' | '-' | '*' | '!' | '=') ;; *) continue ;; esac
    [ -n "$pair" ] || continue
    from="${pair%%:*}"
    to="${pair#*:}"
    case "$from:$to" in
      *sloprail/checks*) refuse "Do not push the sloprail/checks results branch. Only 'sr-checks run' writes it, and it travels with the repository on its own." ;;
    esac
    case "$flag" in '-' | '=' | '!') continue ;; esac
    sha="$(cd "$dir" && git "${GOPTS[@]+"${GOPTS[@]}"}" rev-parse --verify -q "$from^{commit}" 2>/dev/null)" ||
      refuse "The ref '$from' this push would send does not resolve to a commit in $dir, so what leaves could not be checked. Push a branch or commit that exists."
    base="$(cd "$dir" && default_base "$sha")"
    [ -n "$base" ] || refuse "No base for '$from' ($sha) in $dir could be found, so the commits this push would send could not be checked."
    if ! res="$(cd "$dir" && sr-checks verify --base "$base" --head "$sha" 2>&1)"; then
      refuse "This push would send $to ($sha) in $dir, and its commits are not verified clean. Judge them with: cd $dir && sr-checks run --base $base --head $sha — fix what it refuses, commit, run it again, then push. verify said: $(printf '%s' "$res" | head -c 1500)"
    fi
    if (cd "$dir" && results_pending); then
      perr="$(cd "$dir" && push_error)"
      refuse "results not pushed — the judged results of $dir are stored locally only, so the remote and CI would read these commits as not judged. Run: cd $dir && sr-checks run --base $base --head $sha (it retries the push), then push again.${perr:+ The push of the results failed with: $perr} If that keeps failing it is an environment problem (for example no permission to push refs/sloprail/checks) that the USER has to fix: the user can push the results themselves, or turn this gate off in .sloprail/config.yaml. Do not retry in a loop and do not work around it."
    fi
  done <<<"$PUSH_PAIRS"

done
exit 0
