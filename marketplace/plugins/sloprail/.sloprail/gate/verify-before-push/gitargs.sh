# Sourced by verify.sh, cite-before-commit/staged.sh and checks-ref-sr-only/refuse.sh: splits one parsed
# `git` invocation into its global options, its subcommand and the rest, the way git reads them.
# Usage: git_split <invocation-json>   sets GOPTS (array), SUB (string), REST (array), and what the
# command line wrote but the engine could not resolve (a `$(...)`, a variable the line never assigned):
#   GAP_FREE  a word was lost among the global options or where the subcommand stands, and is not the
#             value of an option that takes one. It may have been any option, or the subcommand itself.
#   GAP_BIN   a word was lost in front of `git` (`timeout $T git ...`, `env $E git ...`): it belongs to a
#             wrapper, so it cannot change the subcommand, but the folder and environment git runs in
#             are unknown. A caller that needs them (a push, a commit) refuses it.
#   GAP_VAL   a word was lost where a global option's value stands (`-C $D push`, `-c k=$V`): the folder
#             git runs in (or its config) is unknown.
#   GAP_REST  a word was lost after the subcommand (a remote, a refspec, a path, a message). REST holds
#             a marker word ($'\001gap') at its place.
# Each is "" or 1. The engine reports them as `.gaps` (argv positions), never as an argument, so
# nothing here mistakes the next word for the lost one.
git_split() {
  GOPTS=() SUB="" REST=() GAP_FREE="" GAP_VAL="" GAP_REST="" GAP_BIN=""
  local args=() a gap=$'\001gap' gapbin=$'\001gapbin'
  # NUL-delimited: an argument may hold a newline (a multi-line -m), which a line read would split.
  # A lost word is a marker token at its position.
  while IFS= read -r -d '' a; do args+=("$a"); done < <(printf '%s' "$1" | jq -j '(.argv // []) as $a | (.gaps // []) as $g | [(if ($g | index(0)) != null then "\u0001gapbin" else empty end), range(1;($a | length) + 1) as $i | (if ($g | index($i)) != null then "\u0001gap" else empty end), ($a[$i] // empty)] | .[] | ., "\u0000"')
  local i=0 n=${#args[@]}
  while [ "$i" -lt "$n" ]; do
    a="${args[$i]}"
    case "$a" in
      "$gapbin") GAP_BIN=1 ;;
      "$gap") GAP_FREE=1 ;;
      -C | -c | --git-dir | --work-tree | --namespace | --super-prefix | --config-env | --attr-source)
        GOPTS+=("$a")
        i=$((i + 1))
        if [ "$i" -lt "$n" ]; then
          if [ "${args[$i]}" = "$gap" ]; then
            # any option's value: an unquoted lost word may split into more words at run time
            # (`-c $A push` with A='x=y -C /other'), so it is not known to be only a value
            GAP_VAL=1
            GOPTS+=("")
          elif case "$a" in -C | --git-dir | --work-tree) true ;; *) false ;; esac &&
            { [ "${args[$i]:0:1}" = "~" ] || [[ "${args[$i]}" == *[\*\?\[]* ]]; }; then
            # tilde is not expanded and a glob is not matched by the engine: the word is the shell's to
            # resolve, not a folder
            GAP_VAL=1
            GOPTS+=("")
          else
            GOPTS+=("${args[$i]}")
          fi
        fi
        ;;
      -*) GOPTS+=("$a") ;;
      *)
        SUB="$a"
        i=$((i + 1))
        while [ "$i" -lt "$n" ]; do
          # the marker stays in REST, where the lost word stood, so the word after it is not read as
          # its value (`-m "$M" -a`)
          if [ "${args[$i]}" = "$gap" ]; then GAP_REST=1; fi
          REST+=("${args[$i]}")
          i=$((i + 1))
        done
        return 0
        ;;
    esac
    i=$((i + 1))
  done
}

# git_chdir BASE — applies GOPTS's `-C <dir>` options the way git does (each relative to the one
# before, the first to BASE), sets EDIR to the folder git would run in, and removes them from GOPTS.
# Callers then `cd "$EDIR"` and run every command there, so what git reads (index, refs) and what
# sr-checks reads are the same repository, never the hook's cwd.
git_chdir() {
  # Each hop is entered physically (symlinks resolved), as git's chdir does: `-C a/..` with a -> /x is
  # the parent of /x, not the lexical parent of a.
  EDIR="$(cd -P "$1" 2>/dev/null && pwd -P)" || EDIR="$1"
  local kept=() i=0 n=${#GOPTS[@]} a d next
  while [ "$i" -lt "$n" ]; do
    a="${GOPTS[$i]}"
    if [ "$a" = -C ]; then
      i=$((i + 1))
      d="${GOPTS[$i]-}"
      case "$d" in
        '') ;;
        /*) next="$d" ;;
        *) next="$EDIR/$d" ;;
      esac
      if [ -n "$d" ]; then
        EDIR="$(cd -P "$next" 2>/dev/null && pwd -P)" || EDIR="$next"
      fi
    else
      kept+=("$a")
      case "$a" in -c | --git-dir | --work-tree | --namespace | --super-prefix | --config-env | --attr-source)
        i=$((i + 1))
        kept+=("${GOPTS[$i]-}")
        ;;
      esac
    fi
    i=$((i + 1))
  done
  GOPTS=(${kept[@]+"${kept[@]}"})
}

# git_redirected INV — succeeds when this git invocation (its JSON, from `.event.invocations`) redirects
# git's repository by means the gates do not replay: its `.env` (the parsed assignment prefix, `env`
# wrapper or earlier `export`) names GIT_DIR / GIT_WORK_TREE / GIT_INDEX_FILE / GIT_COMMON_DIR /
# GIT_OBJECT_DIRECTORY / GIT_ALTERNATE_OBJECT_DIRECTORIES / GIT_NAMESPACE, or --git-dir / --work-tree
# is among GOPTS. Nothing here reads the raw command line, so a message that merely MENTIONS GIT_DIR
# is not a redirection. Callers fail closed: the repository git would use is not the folder's.
git_redirected() {
  local a
  printf '%s' "$1" | jq -e '(.env // {}) | keys | any(. as $k | ["GIT_DIR","GIT_WORK_TREE","GIT_INDEX_FILE","GIT_COMMON_DIR","GIT_OBJECT_DIRECTORY","GIT_ALTERNATE_OBJECT_DIRECTORIES","GIT_NAMESPACE"] | index($k))' >/dev/null 2>&1 && return 0
  for a in ${GOPTS[@]+"${GOPTS[@]}"}; do
    case "$a" in --git-dir | --git-dir=* | --work-tree | --work-tree=*) return 0 ;; esac
  done
  return 1
}
gitargs_loaded=1
