#!/usr/bin/env bash
# Refuse a `git push` / `gh pr create` while a file-guard refuses the commits that would
# leave. The evaluation is the engine's (`sr-session judge`); the policy (when to run it,
# and that a refusal blocks the command) is this rule's.
set -uo pipefail

payload="$(cat)"
base="${SR_WORKSPACE:-.}"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

# The first push / pr-create invocation of the line, and where it runs: the directory the
# line says (relative to the workspace), moved by each `git -C <dir>`.
inv="$(printf '%s' "$payload" | jq -c '[.event.invocations[]
  | select((.bin == "git" and (.argv | index("push"))) or (.bin == "gh" and (.argv | index("pr")) and (.argv | index("create"))))][0] // empty')" ||
  refuse "the command line could not be read, so what it pushes could not be judged"
dir="$base"
if [ -n "$inv" ]; then
  cwd="$(printf '%s' "$inv" | jq -r '.cwd // "."')" || cwd="."
  case "$cwd" in
    "" | ".") ;;
    /*) dir="$cwd" ;;
    *) dir="$base/$cwd" ;;
  esac
  prev=""
  while IFS= read -r arg; do
    if [ "$prev" = "-C" ]; then
      case "$arg" in
        /*) dir="$arg" ;;
        *) dir="$dir/$arg" ;;
      esac
    fi
    prev="$arg"
  done < <(printf '%s' "$inv" | jq -r '.argv[]')
fi

if out="$(sr-session judge --dir "$dir" 2>/dev/null)"; then
  exit 0
fi
refuse "this would push commits a file-guard refuses; fix them (and commit) first, then push again:
$out"
