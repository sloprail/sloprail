#!/usr/bin/env bash
# Refuses an agent write to the sloprail/checks ref. Contract: stdin is the GateCheckPayload;
# exit 1 with {"reason": ...} refuses. Fails closed on an unreadable event.
#
# SCOPE: this guards against an agent's ACCIDENTAL write to the results ref, not a determined forger.
# It reads the command's argv; a ref name the shell builds at run time ("$(printf refs/sloprail/ch)ecks")
# is not in the argv, so no argv parsing can catch it. The ref's integrity against a forger comes from
# `sr-checks verify` being re-run in CI on a clone, not from this gate.
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
# refspec_hits SPEC fetch|push — succeeds when SPEC (`[+]src:dst`, globs allowed) has a destination
# that can be the results ref (refs/sloprail/checks); a push can also reach the remote's branch
# (refs/heads/sloprail/checks). A short destination is completed the way git completes it.
refspec_hits() {
  local a="${1#+}" dst c t targets="refs/sloprail/checks"
  [ "${2:-fetch}" = push ] && targets="refs/sloprail/checks refs/heads/sloprail/checks"
  case "$a" in *:*) dst="${a##*:}" ;; *) return 1 ;; esac
  [ -n "$dst" ] || return 1
  for c in "$dst" "refs/$dst" "refs/heads/$dst" "refs/tags/$dst"; do
    for t in $targets; do
      # shellcheck disable=SC2053
      [[ "$t" == $c ]] && return 0
    done
  done
  return 1
}
reads=" log show rev-parse rev-list cat-file ls-tree show-ref for-each-ref diff diff-tree merge-base name-rev describe ls-remote reflog grep blame "
i=0
while [ "$i" -lt "$n" ]; do
  inv="$(printf '%s' "$payload" | jq -c --argjson i "$i" '.event.invocations[$i]')" || refuse "an invocation could not be read, so this could not be checked"
  i=$((i + 1))
  [ "$(printf '%s' "$inv" | jq -r '.bin // ""')" = "git" ] || continue
  git_split "$inv"
  # A word the engine could not resolve (a `$(...)`, a variable the line did not assign to a literal)
  # may be the subcommand or the ref: it could name the results ref, and would not be read here.
  [ -z "$GAP_FREE" ] || refuse "A 'git' command on this line has an option or subcommand that is a variable, \$(...) or ~ the line does not assign to a literal, so it could not be checked against the results ref. Use the literal subcommand and options. $msg"
  case "$SUB" in
    update-ref | symbolic-ref | push | fetch | pull | fast-import)
      [ -z "$GAP_BIN" ] || refuse "A word in front of this 'git $SUB' (a wrapper's option or environment held in a variable or \$(...)) could not be resolved, so it could not be checked against the results ref. Write the command without it. $msg"
      [ -z "$GAP_REST" ] || refuse "This 'git $SUB' names a ref through a variable or \$(...) that could not be resolved, so it could not be checked against the results ref. Write the ref out literally. $msg"
      ;;
  esac
  # forms that write a ref without naming it: the ref is in the piped input (update-ref --stdin;
  # sr-checks writes the ref through its own process, never through an agent's git command), or it is
  # the destination of a refspec ('refs/*:refs/*', '+*:*', an option `-c remote.x.fetch=...`, a
  # `remote add --mirror`). Only a refspec whose DESTINATION can reach the ref counts: a fetch of
  # 'refs/tags/*:refs/tags/*' or a push of 'feat-*:feat-*' is ordinary.
  named="" glob=""
  printf '%s' "$inv" | jq -e 'any(.argv[]; contains("sloprail/checks"))' >/dev/null 2>&1 && named=1
  case "$SUB" in
    fast-import)
      # the ref it writes (and --force) is in its input stream, which is not visible here
      named=1
      ;;
    update-ref)
      for a in ${REST[@]+"${REST[@]}"}; do [ "$a" = "--stdin" ] && named=1; done
      ;;
    fetch | push | pull)
      mode=fetch
      [ "$SUB" = push ] && mode=push
      for a in ${REST[@]+"${REST[@]}"}; do refspec_hits "$a" "$mode" && named=1 glob=1; done
      ;;
    remote)
      for a in ${REST[@]+"${REST[@]}"}; do case "$a" in --mirror | --mirror=*) named=1 glob=1 ;; esac; done
      ;;
    config)
      # a persistent refspec / mirror setting on a remote (`git config [--add|set] remote.x.fetch <refspec>`)
      keyed=""
      for a in ${REST[@]+"${REST[@]}"}; do case "$a" in remote.*.mirror) named=1 glob=1 ;; remote.*.fetch) keyed=fetch ;; remote.*.push) keyed=push ;; esac; done
      if [ -n "$keyed" ]; then
        for a in ${REST[@]+"${REST[@]}"}; do refspec_hits "$a" "$keyed" && named=1 glob=1; done
      fi
      ;;
  esac
  # `git -c remote.x.fetch=<refspec> fetch`, `-c remote.x.push=<refspec>`, `-c remote.x.mirror=true`
  gi=0
  while [ "$gi" -lt "${#GOPTS[@]}" ]; do
    if [ "${GOPTS[$gi]}" = "-c" ] || [ "${GOPTS[$gi]}" = "--config-env" ]; then
      gi=$((gi + 1))
      kv="${GOPTS[$gi]:-}"
      case "$kv" in
        remote.*.mirror=*) case "${kv#*=}" in false | no | off | 0) ;; *) named=1 glob=1 ;; esac ;;
        remote.*.fetch=*) refspec_hits "${kv#*=}" fetch && named=1 glob=1 ;;
        remote.*.push=*) refspec_hits "${kv#*=}" push && named=1 glob=1 ;;
        remote.*.fetch | remote.*.push) named=1 glob=1 ;; # --config-env: the value is not visible
      esac
    fi
    gi=$((gi + 1))
  done
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
