#!/usr/bin/env bash
# The cite-before-commit gate's one script, in two roles (the first argument):
#   when   the `require: citation`'s `when`: exit 0 applies it (some staged file needs a citation, or it
#          could not be told), exit 1 waives it. On exit 0 prints {"hint": ...}: the command to run.
#   check  runs after the requirement is met: refuses (exit 1, {"reason": ...}) when the engine could
#          not say which files the commit needs cited. Fail closed.
# Stdin is the GateCheckPayload. The commit's candidate change is read by `sr-checks staged`.
set -uo pipefail

mode="${1:-check}"
payload="$(cat)"
lib_dir="$(cd "$(dirname "$0")" && pwd)"
cmds="sr-session trajectory cite '<exact quote>' && git commit -m '<what changed>' -m 'Sloprail-Cites-User: <exact quote>'"

# fail MSG: `when` cannot refuse, so it lets the requirement apply (a cite is then asked for, and
# `check` refuses the same fault once a cite is there); `check` refuses.
fail() {
  if [ "$mode" = when ]; then
    jq -n --arg h "cite-before-commit could not check this commit ($1). Cite before you commit:
  $cmds" '{hint: $h}'
    exit 0
  fi
  jq -n --arg r "This commit could not be checked for citations: $1" '{reason: $r}'
  exit 1
}

unset gitargs_loaded
. "$lib_dir/../verify-before-push/gitargs.sh" || fail "its helpers did not load"
[ "${gitargs_loaded:-}" = 1 ] || fail "its helpers loaded only partly"
command -v sr-checks >/dev/null 2>&1 || fail "sr-checks is not on PATH; install the sloprail plugin's binaries"

n="$(printf '%s' "$payload" | jq -r '.event.invocations | length')" || n=""
case "$n" in '' | *[!0-9]*) fail "the command's invocations could not be read" ;; esac

# drop_unsafe_gopts: removes the global options that make git run agent-chosen code (-c,
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
split_git() { git_split "$1"; drop_unsafe_gopts; }

# Commands that move the index (or HEAD) and cannot be replayed on a throwaway index. The gate runs
# before the line, so a commit chained after one would be judged against the wrong change.
movers=" mv restore reset checkout switch cherry-pick pull am revert apply merge rebase stash clean update-index read-tree "

tmpdirs=()
cleanup() { [ "${#tmpdirs[@]}" -eq 0 ] || rm -rf "${tmpdirs[@]}"; }
trap cleanup EXIT

invs=()
i=0
while [ "$i" -lt "$n" ]; do
  inv="$(printf '%s' "$payload" | jq -c --argjson i "$i" '.event.invocations[$i]')" ||
    fail "invocation $i of the command could not be read"
  invs+=("$inv")
  i=$((i + 1))
done

# dir_of INV: the folder an invocation runs in.
dir_of() {
  local cwd
  cwd="$(printf '%s' "$1" | jq -r '.cwd // ""')"
  [ -n "$cwd" ] || return 1
  case "$cwd" in /*) printf '%s' "$cwd" ;; *) printf '%s' "${SR_WORKSPACE:-.}/$cwd" ;; esac
}

files=()
unresolved=""
amending=""
idx=0
for inv in "${invs[@]}"; do
  idx=$((idx + 1))
  [ "$(printf '%s' "$inv" | jq -r '.bin // ""')" = git ] || continue
  split_git "$inv"
  [ "$SUB" = commit ] || continue

  amend="" all="" include="" newmsg="" dry="" help="" nopath="" paths=() msgs=() msgfiles=()
  args=(${REST[@]+"${REST[@]}"})
  j=0
  while [ "$j" -lt "${#args[@]}" ]; do
    a="${args[$j]}"
    j=$((j + 1))
    if [ -n "$nopath" ]; then
      paths+=("$a")
      continue
    fi
    case "$a" in
      --) nopath=1 ;;
      --amend) amend=1 ;;
      --all) all=1 ;;
      --include) include=1 ;;
      --dry-run) dry=1 ;;
      -h | --help) help=1 ;;
      --edit) newmsg=1 ;;
      --message | --trailer)
        newmsg=1
        msgs+=("${args[$j]-}")
        j=$((j + 1))
        ;;
      --file)
        newmsg=1
        msgfiles+=("${args[$j]-}")
        j=$((j + 1))
        ;;
      --reuse-message | --reedit-message)
        newmsg=1
        j=$((j + 1))
        ;;
      --message=* | --trailer=*)
        newmsg=1
        msgs+=("${a#*=}")
        ;;
      --file=*)
        newmsg=1
        msgfiles+=("${a#*=}")
        ;;
      --reuse-message=* | --reedit-message=* | --fixup=* | --squash=*) newmsg=1 ;;
      --author | --date | --cleanup | --template) j=$((j + 1)) ;;
      --*) ;;
      -?*)
        # A short-option cluster: -a, -am 'msg', -mfoo. A value-taking letter ends the cluster.
        cl="${a#-}"
        while [ -n "$cl" ]; do
          c="${cl:0:1}"
          cl="${cl:1}"
          case "$c" in
            a) all=1 ;;
            i) include=1 ;;
            e) newmsg=1 ;;
            m)
              newmsg=1
              if [ -n "$cl" ]; then msgs+=("$cl"); else msgs+=("${args[$j]-}"); j=$((j + 1)); fi
              cl=""
              ;;
            F)
              newmsg=1
              if [ -n "$cl" ]; then msgfiles+=("$cl"); else msgfiles+=("${args[$j]-}"); j=$((j + 1)); fi
              cl=""
              ;;
            C | c)
              newmsg=1
              [ -n "$cl" ] || j=$((j + 1))
              cl=""
              ;;
            t)
              [ -n "$cl" ] || j=$((j + 1))
              cl=""
              ;;
          esac
        done
        ;;
      *) paths+=("$a") ;;
    esac
  done
  [ -z "$help" ] || continue
  [ -z "$dry" ] || continue
  [ -z "$amend" ] || amending=1

  dir="$(dir_of "$inv")" || fail "the folder the commit runs in could not be told from the command line (a cd to a variable, an eval); run it as 'git -C <dir> commit ...' with a literal folder"
  [ -d "$dir" ] || fail "the folder the commit runs in ($dir) does not exist"
  top="$(cd "$dir" && git "${GOPTS[@]+"${GOPTS[@]}"}" rev-parse --show-toplevel 2>/dev/null)" || fail "$dir is not inside a git repository"

  # The index the commit will build from, on a throwaway copy: the real one is never touched.
  tmp="$(mktemp -d)" || fail "a scratch folder could not be made"
  tmpdirs+=("$tmp")
  real="$(cd "$dir" && git "${GOPTS[@]+"${GOPTS[@]}"}" rev-parse --path-format=absolute --git-path index 2>/dev/null)" || fail "the index of $dir could not be located"
  hashead=""
  (cd "$dir" && git "${GOPTS[@]+"${GOPTS[@]}"}" rev-parse --verify -q 'HEAD^{commit}' >/dev/null 2>&1) && hashead=1
  if [ "${#paths[@]}" -gt 0 ] && [ -z "$include" ]; then
    # `git commit <paths>` commits those paths as the work tree has them, over HEAD, ignoring the index.
    if [ -n "$hashead" ]; then
      (cd "$dir" && GIT_INDEX_FILE="$tmp/index" git "${GOPTS[@]+"${GOPTS[@]}"}" read-tree HEAD) >/dev/null 2>&1 || fail "HEAD could not be read into a scratch index"
    fi
  else
    [ ! -e "$real" ] || cp "$real" "$tmp/index" || fail "the index could not be copied"
  fi

  # What the line itself stages before this commit: replayed on the scratch index, in order.
  k=0
  for prev in "${invs[@]}"; do
    k=$((k + 1))
    [ "$k" -lt "$idx" ] || break
    [ "$(printf '%s' "$prev" | jq -r '.bin // ""')" = git ] || continue
    split_git "$prev"
    pdir="$(dir_of "$prev")" || continue
    ptop="$(cd "$pdir" 2>/dev/null && git "${GOPTS[@]+"${GOPTS[@]}"}" rev-parse --show-toplevel 2>/dev/null)" || continue
    [ "$ptop" = "$top" ] || continue
    if [ "$SUB" = add ]; then
      (cd "$pdir" && GIT_INDEX_FILE="$tmp/index" git "${GOPTS[@]+"${GOPTS[@]}"}" add ${REST[@]+"${REST[@]}"} </dev/null) >/dev/null 2>&1 ||
        fail "the 'git add' this line runs before the commit could not be replayed"
    elif [ "$SUB" = rm ]; then
      (cd "$pdir" && GIT_INDEX_FILE="$tmp/index" git "${GOPTS[@]+"${GOPTS[@]}"}" rm --cached --quiet ${REST[@]+"${REST[@]}"} </dev/null) >/dev/null 2>&1 ||
        fail "the 'git rm' this line runs before the commit could not be replayed"
    elif [ -n "$SUB" ] && [[ "$movers" == *" $SUB "* ]]; then
      fail "this line runs 'git $SUB' and 'git commit' together, and the commit is checked before the line runs; run 'git $SUB' first, then the commit as its own command"
    fi
  done
  split_git "$inv"

  if [ -n "$all" ]; then
    (cd "$dir" && GIT_INDEX_FILE="$tmp/index" git "${GOPTS[@]+"${GOPTS[@]}"}" add -u </dev/null) >/dev/null 2>&1 || fail "'git commit -a' could not be replayed on a scratch index"
  fi
  if [ "${#paths[@]}" -gt 0 ]; then
    (cd "$dir" && GIT_INDEX_FILE="$tmp/index" git "${GOPTS[@]+"${GOPTS[@]}"}" add -A -- "${paths[@]}" </dev/null) >/dev/null 2>&1 || fail "the paths this commit names could not be replayed on a scratch index"
  fi

  flags=(--needs citation)
  [ -z "$amend" ] || flags+=(--amend)
  if ! out="$(cd "$dir" && GIT_INDEX_FILE="$tmp/index" sr-checks staged "${flags[@]}" 2>"$tmp/err")"; then
    fail "sr-checks staged said: $(head -c 800 "$tmp/err")"
  fi

  # The citations the commit's own message carries: -m / -F / --trailer values (a -F file is read from
  # the commit's folder), or, for an amend that reuses HEAD's message, HEAD's. Each quote is resolved
  # against the session transcript by `sr-checks staged --trailers` (the resolver `sr-file --cite` and
  # `sr-session trajectory cite` use). A commit's resolving citation grounds every file it changes;
  # a quote that does not resolve grounds nothing and is named in the refusal.
  if [ -n "$out" ]; then
    msgtext=""
    if [ -n "$amend" ] && [ -z "$newmsg" ]; then
      msgtext="$(cd "$dir" && git "${GOPTS[@]+"${GOPTS[@]}"}" log -1 --format=%B HEAD 2>/dev/null)" || fail "HEAD's message could not be read"
    else
      for m in ${msgs[@]+"${msgs[@]}"}; do msgtext="$msgtext$m"$'\n'; done
      for mf in ${msgfiles[@]+"${msgfiles[@]}"}; do
        [ "$mf" != - ] || continue
        case "$mf" in /*) ;; *) mf="$dir/$mf" ;; esac
        msgtext="$msgtext$(cat "$mf" 2>/dev/null)"$'\n'
      done
    fi
    if printf '%s' "$msgtext" | grep -Eiq '^Sloprail-Cites-(User|Tool):'; then
      res="$(printf '%s' "$msgtext" | (cd "$dir" && sr-checks staged --trailers 2>"$tmp/err"))" ||
        fail "sr-checks staged --trailers said: $(head -c 800 "$tmp/err")"
      good="$(printf '%s\n' "$res" | jq -rs '[.[] | select(.ok)] | length' 2>/dev/null)" || fail "the resolved citations could not be read"
      if [ "${good:-0}" -gt 0 ]; then
        out=""
      fi
      bad="$(printf '%s\n' "$res" | jq -rs '[.[] | select(.ok | not)] | map("  \(.trailer): \(.quote): \(.error)") | join("\n")' 2>/dev/null)" || fail "the resolved citations could not be read"
      [ -z "$bad" ] || unresolved="$unresolved$bad"$'\n'
    fi
  fi
  while IFS= read -r f; do
    [ -z "$f" ] || files+=("$f")
  done <<<"$out"
done

if [ "${#files[@]}" -eq 0 ]; then
  # Nothing to cite: the `when` waives the requirement (exit 1), the check has nothing to refuse.
  [ "$mode" = when ] && exit 1
  exit 0
fi
if [ "$mode" != when ]; then
  # A chained cite met the requirement, but a trailer on the commit that does not resolve is refused here:
  # it would carry the commit past this gate and fail at Stop and in CI.
  if [ -n "$unresolved" ]; then
    jq -n --arg r "The citation trailers on this commit did not resolve against the session, so they ground nothing:
$unresolved" '{reason: $r}'
    exit 1
  fi
  exit 0
fi

list="$(printf '%s\n' "${files[@]}" | sort -u | paste -sd, - | sed 's/,/, /g')"
if [ -n "$amending" ]; then
  how="sr-session trajectory cite '<exact quote>' && git commit --amend --no-edit --trailer 'Sloprail-Cites-User: <exact quote>'"
else
  how="$cmds"
fi
if [ -n "$unresolved" ]; then
  how="$how
The citation trailers on this commit did not resolve against the session, so they ground nothing:
$unresolved"
fi
# Quotes the session already recorded for these files (sr-file --cite): the agent has found them; they
# only need to ride on the commit. Best effort: a failure here leaves the hint without them.
rec=""
if [ "${#invs[@]}" -gt 0 ] && rdir="$(dir_of "${invs[0]}")"; then
  rec="$(cd "$rdir" && sr-checks staged --recorded "${files[@]}" 2>/dev/null | jq -r '"  recorded for \(.path): -m \"\(.trailer): \(.quote)\"" ' 2>/dev/null)" || rec=""
fi
[ -z "$rec" ] || how="$how
Quotes this session already recorded for these files (carry one as a trailer, still chained behind a cite):
$rec"
jq -n --arg h "This commit changes files a file-guard requires a citation for: $list. Quote what grounds the change (the user's words, or a tool's output with --source-types tool_result) in front of the commit, and carry the same quote as a trailer on it: the file-guard checks the trailer at Stop and in CI.
  $how
(Use Sloprail-Cites-Tool for a tool's output.)" '{hint: $h}'
exit 0
