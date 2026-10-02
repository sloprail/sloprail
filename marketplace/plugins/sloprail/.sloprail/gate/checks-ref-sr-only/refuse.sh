#!/usr/bin/env bash
# Refuses an agent write to the sloprail/checks ref. Contract: stdin is the GateCheckPayload;
# exit 1 with {"reason": ...} refuses. Fails closed on an unreadable event.
set -uo pipefail

payload="$(cat)"
refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}
msg="The sloprail/checks branch holds the verdicts of 'sr-checks run', and only sr-checks writes it: do not write, move, delete, fetch into or push it. To judge commits run 'sr-checks run --base <rev> --head <rev>'; to read the verdicts run 'sr-checks status' or 'sr-checks verify'."

kind="$(printf '%s' "$payload" | jq -r '.event.kind // ""')" || kind=""
case "$kind" in
  PreFileCreate | PreFileUpdate | PreFileDelete) refuse "$msg" ;;
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
  printf '%s' "$inv" | jq -e 'any(.argv[]; contains("sloprail/checks"))' >/dev/null 2>&1 || continue
  git_split "$inv"
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
      [ -z "$colon" ] && continue
      ;;
  esac
  refuse "$msg"
done
exit 0
