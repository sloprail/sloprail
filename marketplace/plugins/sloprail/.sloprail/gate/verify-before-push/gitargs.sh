# Sourced by verify.sh, cite-before-commit/staged.sh and checks-ref-sr-only/refuse.sh: splits one parsed
# `git` invocation into its global options, its subcommand and the rest, the way git reads them.
# Usage: git_split <invocation-json>   sets GOPTS (array), SUB (string), REST (array), and what the
# command line wrote but the engine could not resolve (a `$(...)`, a variable the line never assigned):
#   GAP_FREE  a word was lost among the global options or where the subcommand stands, and is not the
#             value of an option that takes one. It may have been any option, or the subcommand itself.
#   GAP_VAL   a word was lost where a global option's value stands (`-C $D push`): the folder (or
#             config, or git-dir) git runs with is unknown.
#   GAP_REST  a word was lost after the subcommand (a remote, a refspec, a path, a message).
# Each is "" or 1. The engine reports them as `.gaps` (argv positions), never as an argument, so
# nothing here mistakes the next word for the lost one.
git_split() {
  GOPTS=() SUB="" REST=() GAP_FREE="" GAP_VAL="" GAP_REST=""
  local args=() a gap=$'\001gap'
  # NUL-delimited: an argument may hold a newline (a multi-line -m), which a line read would split.
  # A lost word is a marker token at its position.
  while IFS= read -r -d '' a; do args+=("$a"); done < <(printf '%s' "$1" | jq -j '(.argv // []) as $a | (.gaps // []) as $g | [range(1; ($a | length) + 1) as $i | (if ($g | index($i)) != null then "\u0001gap" else empty end), ($a[$i] // empty)] | .[] | ., "\u0000"')
  local i=0 n=${#args[@]}
  while [ "$i" -lt "$n" ]; do
    a="${args[$i]}"
    case "$a" in
      "$gap") GAP_FREE=1 ;;
      -C | -c | --git-dir | --work-tree | --namespace | --super-prefix | --config-env)
        GOPTS+=("$a")
        i=$((i + 1))
        if [ "$i" -lt "$n" ]; then
          if [ "${args[$i]}" = "$gap" ]; then
            GAP_VAL=1
            GOPTS+=("")
          elif [ "${args[$i]:0:1}" = "~" ]; then
            # tilde is not expanded by the engine: the word is the shell's to resolve, not a folder
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
          if [ "${args[$i]}" = "$gap" ]; then GAP_REST=1; else REST+=("${args[$i]}"); fi
          i=$((i + 1))
        done
        return 0
        ;;
    esac
    i=$((i + 1))
  done
}

# git_unresolved — succeeds when the last git_split'ed invocation lost a word that decides WHICH git
# command runs or WHERE (GAP_FREE: an option or the subcommand; GAP_VAL: an option's value). Callers
# refuse: the command is not the one this gate would judge.
git_unresolved() {
  [ -n "${GAP_FREE:-}" ] || [ -n "${GAP_VAL:-}" ]
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
      case "$a" in -c | --git-dir | --work-tree | --namespace | --super-prefix | --config-env)
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
