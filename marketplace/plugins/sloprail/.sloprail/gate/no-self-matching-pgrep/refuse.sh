#!/usr/bin/env bash
# The gate's match found a pgrep/pkill among the line's invocations. Refuse one that has -f/--full
# and whose pattern names a sloprail program (sr-checks, sr-session, ... or an `sr ` subcommand).
# Contract: stdin is the GateCheckPayload; exit 1 with {"reason": ...} refuses.
set -uo pipefail

payload="$(cat)"
reason='pgrep -f matches its own shell, so waiting on our own sloprail commands this way never ends (and it sees every run on the machine). Run the sloprail command in the foreground (sr-checks run reports progress), or wait on its own PID: `cmd & pid=$!; wait $pid`.'

sr_re='(^|[^[:alnum:]_.-])(sr-(checks|session|agent|file|mark|eval)([^[:alnum:]_]|$)|sr )'

# Options of pgrep/pkill that take a separate value, so it is not mistaken for the pattern.
valopts=" -u -U -g -G -P -s -t -d -F -c -O -T -M --euid --uid --pgroup --group --parent --session --terminal --delimiter --pidfile --ns --nslist --signal "

while IFS= read -r inv; do
  bin="$(printf '%s' "$inv" | jq -r '.bin')"
  case "$bin" in pgrep | pkill) ;; *) continue ;; esac
  full=0 pat="" skip=0 dd=0
  while IFS= read -r a; do
    a="$(printf '%s' "$a" | jq -r '.')"
    if [ "$skip" = 1 ]; then
      skip=0
      continue
    fi
    if [ "$dd" = 1 ]; then
      [ -z "$pat" ] && pat="$a"
      continue
    fi
    case "$a" in
      --) dd=1 ;;
      --full) full=1 ;;
      --*) [[ "$valopts" == *" $a "* ]] && skip=1 ;;
      -?*)
        [[ "$a" == -*f* ]] && full=1
        # a value-taking short option ends a cluster; as the last letter its value is the next word
        for ((k = 1; k < ${#a}; k++)); do
          if [[ "$valopts" == *" -${a:k:1} "* ]]; then
            [ $((k + 1)) -ge ${#a} ] && skip=1
            break
          fi
        done
        ;;
      *) [ -z "$pat" ] && pat="$a" ;;
    esac
  done < <(printf '%s' "$inv" | jq -c '(.argv // [])[1:][]')
  [ "$full" = 1 ] && [ -n "$pat" ] || continue

  if [[ "$pat" =~ $sr_re ]]; then
    jq -n --arg r "$reason" '{reason: $r}'
    exit 1
  fi
done < <(printf '%s' "$payload" | jq -c '.event.invocations[]?')
exit 0
