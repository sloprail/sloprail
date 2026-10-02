# Sourced by verify.sh and by checks-ref-sr-only/refuse.sh: splits one parsed `git` invocation
# into its global options, its subcommand and the rest, the way git reads them.
# Usage: git_split <invocation-json>   sets GOPTS (array), SUB (string), REST (array).
git_split() {
  GOPTS=() SUB="" REST=()
  local args=() a
  while IFS= read -r a; do args+=("$a"); done < <(printf '%s' "$1" | jq -r '(.argv // [])[1:][]')
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
gitargs_loaded=1
