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
# a new remote, no origin/HEAD) has none to ask: the session's recorded base for the range being
# pushed stands in when there is one (an ancestor of SHA), else the whole history is judged from the
# root commit. Never an empty range, and never a push that cannot be pushed.
default_base() {
  local sha="$1" b rec
  if b="$(sr-checks default-base --head "$sha" 2>/dev/null)"; then
    if [ "$b" = "4b825dc642cb6eb9a060e54bf8d69288fbee4904" ]; then
      root_commit "$sha"
      return
    fi
    printf '%s' "$b"
    return
  fi
  rec="$(sr-session refs list --json 2>/dev/null | jq -r --arg sha "$sha" --arg dir "$PWD" \
    '[.[] | select(.HeadSHA == $sha and .Base != "" and (.UntrackedReason // "") == "") | .Base][0] // empty' 2>/dev/null)"
  if [ -n "$rec" ] && [ "$rec" != "4b825dc642cb6eb9a060e54bf8d69288fbee4904" ] &&
    git "${GOPTS[@]+"${GOPTS[@]}"}" merge-base --is-ancestor "$rec" "$sha" 2>/dev/null; then
    printf '%s' "$rec"
    return
  fi
  root_commit "$sha"
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

  # -q / --quiet would hide the porcelain lines this reads, so they are not passed on.
  dry=()
  for a in ${REST[@]+"${REST[@]}"}; do case "$a" in -q | --quiet) ;; *) dry+=("$a") ;; esac; done
  err="$(mktemp)"
  out="$(cd "$dir" && git "${GOPTS[@]+"${GOPTS[@]}"}" push --dry-run --porcelain --no-verify ${dry[@]+"${dry[@]}"} 2>"$err")"
  status=$?
  why="$(head -c 600 "$err")"
  rm -f "$err"

  refs=0
  while IFS=$'\t' read -r flag pair _; do
    case "$flag" in ' ' | '+' | '-' | '*' | '!' | '=') ;; *) continue ;; esac
    [ -n "$pair" ] || continue
    refs=$((refs + 1))
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
  done <<<"$out"

  if [ "$refs" -eq 0 ] && [ "$status" -ne 0 ]; then
    refuse "This push could not be resolved to the refs it would update in $dir, so what leaves could not be checked: ${why:-git exited $status}"
  fi
done
exit 0
