#!/usr/bin/env bash
# Refuses an agent write to the sloprail/checks ref. Contract: stdin is the GateCheckPayload;
# exit 1 with {"reason": ...} refuses. Fails closed on an unreadable event.
set -uo pipefail

payload="$(cat)"
refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}
msg="The sloprail/checks branch holds the verdicts of 'sr-checks run', and only sr-checks writes it: do not write, move, delete, fetch into or push it. To judge commits run 'sr-checks run --base <rev> --head <rev>'; to read the verdicts run 'sr-checks show' or 'sr-checks verify'."

kind="$(printf '%s' "$payload" | jq -r '.event.kind // ""')" || kind=""
case "$kind" in
  PreFileCreate | PreFileUpdate | PreFileDelete)
    # packed-refs is the one path that does not name the ref: it is refused only when it is THIS
    # repository's (a copy of some other clone's .git is not the results branch)
    path="$(printf '%s' "$payload" | jq -r '.event.path // ""')" || path=""
    case "$path" in
      *refs/sloprail/checks* | *refs/heads/sloprail/checks*) refuse "$msg" ;;
    esac
    base="${SR_WORKSPACE:-$PWD}"
    case "$path" in /*) ;; *) path="$base/$path" ;; esac
    common="$(git -C "$base" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)" || refuse "$msg"
    [ -n "$common" ] || refuse "$msg"
    pdir="$(cd "$(dirname "$path")" 2>/dev/null && pwd -P)" || pdir=""
    cdir="$(cd "$common" 2>/dev/null && pwd -P)" || cdir=""
    # a parent that does not exist yet cannot be this repository's .git
    if [ -n "$pdir" ] && [ "$pdir" != "$cdir" ]; then exit 0; fi
    [ -n "$pdir" ] || exit 0
    refuse "$msg"
    ;;
  PreCommandInvoke) ;;
  *) refuse "unexpected event kind '$kind', so this could not be checked" ;;
esac

lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset gitargs_loaded
. "$lib_dir/../verify-before-push/gitargs.sh" || refuse "could not load the git argument helper, so this could not be checked"
[ "${gitargs_loaded:-}" = 1 ] || refuse "the git argument helper loaded only partly, so this could not be checked"

n="$(printf '%s' "$payload" | jq -r '.event.invocations | length')" || n=""
case "$n" in '' | *[!0-9]*) refuse "the command's invocations could not be read, so this could not be checked" ;; esac
reads=" log show rev-parse rev-list cat-file ls-tree show-ref for-each-ref diff diff-tree merge-base name-rev describe ls-remote reflog grep blame "
i=0
while [ "$i" -lt "$n" ]; do
  inv="$(printf '%s' "$payload" | jq -c --argjson i "$i" '.event.invocations[$i]')" || refuse "an invocation could not be read, so this could not be checked"
  i=$((i + 1))
  [ "$(printf '%s' "$inv" | jq -r '.bin // ""')" = "git" ] || continue
  git_split "$inv"
  # forms that write a ref without naming it: the ref is in the piped input (update-ref --stdin;
  # sr-checks writes the ref through its own process, never through an agent's git command), or in a
  # glob refspec (fetch/push 'refs/*:refs/*', 'refs/sloprail/*:refs/sloprail/*')
  named="" glob=""
  printf '%s' "$inv" | jq -e 'any(.argv[]; contains("sloprail/checks"))' >/dev/null 2>&1 && named=1
  case "$SUB" in
    update-ref)
      for a in ${REST[@]+"${REST[@]}"}; do [ "$a" = "--stdin" ] && named=1; done
      ;;
    fetch | push | pull)
      for a in ${REST[@]+"${REST[@]}"}; do case "$a" in *:refs/remotes/*) ;; *\**:* | *:*\**) named=1 glob=1 ;; esac; done
      ;;
  esac
  [ -n "$named" ] || continue
  [[ "$reads" == *" $SUB "* ]] && continue
  case "$SUB" in
    branch)
      # a listing is a read; anything else names the branch to create, move or delete
      listing=""
      for a in ${REST[@]+"${REST[@]}"}; do case "$a" in -l | --list | -a | -r | -v | -vv | --show-current) listing=1 ;; esac; done
      [ -n "$listing" ] && continue
      ;;
    fetch)
      # `git fetch origin sloprail/checks` only fills FETCH_HEAD; a refspec with a destination writes a ref
      colon=""
      for a in ${REST[@]+"${REST[@]}"}; do case "$a" in *sloprail/checks*:*) colon=1 ;; esac; done
      [ -z "$colon" ] && [ -z "$glob" ] && continue
      ;;
  esac
  refuse "$msg"
done
exit 0
