# Sourced by verify.sh and by checks-ref-sr-only/refuse.sh: splits one parsed `git` invocation
# into its global options, its subcommand and the rest, the way git reads them.
# Usage: git_split <invocation-json>   sets GOPTS (array), SUB (string), REST (array).
git_split() {
  GOPTS=() SUB="" REST=()
  local args=() a
  # NUL-delimited: an argument may hold a newline (a multi-line -m), which a line read would split.
  while IFS= read -r -d '' a; do args+=("$a"); done < <(printf '%s' "$1" | jq -j '(.argv // [])[1:][] | ., "\u0000"')
  local i=0 n=${#args[@]}
  while [ "$i" -lt "$n" ]; do
    a="${args[$i]}"
    case "$a" in
      -C | -c | --git-dir | --work-tree | --namespace | --super-prefix | --config-env)
        GOPTS+=("$a")
        i=$((i + 1))
        [ "$i" -lt "$n" ] && GOPTS+=("${args[$i]}")
        ;;
      -*) GOPTS+=("$a") ;;
      *)
        SUB="$a"
        i=$((i + 1))
        while [ "$i" -lt "$n" ]; do
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
  EDIR="$1"
  local kept=() i=0 n=${#GOPTS[@]} a d
  while [ "$i" -lt "$n" ]; do
    a="${GOPTS[$i]}"
    if [ "$a" = -C ]; then
      i=$((i + 1))
      d="${GOPTS[$i]-}"
      case "$d" in
        '') ;;
        /*) EDIR="$d" ;;
        *) EDIR="$EDIR/$d" ;;
      esac
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
gitargs_loaded=1
