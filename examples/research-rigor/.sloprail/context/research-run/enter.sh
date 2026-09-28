#!/usr/bin/env bash
# enter: a #research tag or dispatch (the triggers' match already confirmed
# it) opens a declared run. A file write opens one only if it added a
# "Proposed approach" section — the findings — was not already seen at an
# earlier Stop, and was written by this trajectory or one it dispatched; an
# active run stays as it is. See context.md — a clean exit ACTIVATES, so every
# "no" here exits non-zero.
set -uo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
input="$(cat)"
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"

case "$kind" in
  PostFileCreate | PostFileUpdate) ;;
  *)
    jq -n '{declared: true}'
    exit 0
    ;;
esac

# Declining is a NON-ZERO exit: it leaves the context exactly as it was.
# (A clean exit with no output would activate it, keeping the old payload.)
# Already open: keep what opened it — a declared run stays declared.
[ "$(printf '%s' "$input" | jq -r '.currentContext.active // false')" != "true" ] || exit 1
adds="$(printf '%s' "$input" | jq -r -L "$here/../../gate/findings-need-depth" 'include "proposal";
  if .event.seen == true then false else (.event | adds_proposal) end' 2>/dev/null)"
[ "$adds" = "true" ] || exit 1

# One spelling per file (/var vs /private/var), for comparing trajectories.
rp() { realpath -q -- "$1" 2>/dev/null || printf '%s' "$1"; }
open() { printf '%s' "$input" | jq -c '{declared: false, proposal: .event.path}'; exit 0; }

# Who owes the research for this proposal? A Stop sees every file that
# changed in the session, so a sub-agent whose dispatcher wrote NOTES.md must
# not be asked for it — but the dispatcher of a sub-agent that wrote it must:
# the sub-agent's own refusals end when its Stop cap does, and the research was
# the dispatcher's to see done. So the proposal belongs to the trajectory that
# wrote it AND every trajectory above it (its parentPath chain), never to a
# sibling or one below. A write no call names is owed by the calls of this
# cycle that could have made it unseen (an interpreter, a script, eval of a
# variable); with none of those either, it is not the agent's (a user's edit,
# git bringing in committed content). Anything that cannot be read opens the
# run (fail closed).
me="$(printf '%s' "$input" | jq -r '.transcriptPath // empty')"
path="$(printf '%s' "$input" | jq -r '.event.path // empty' | tr '[:upper:]' '[:lower:]')"
[ -n "$me" ] || open
me="$(rp "$me")"

parent_of() { sr-session trajectory describe --path "$1" 2>/dev/null | jq -r '.parentPath // empty' 2>/dev/null; }

# The session's root, and every record of the session.
root="$me"
for _ in 1 2 3 4 5 6 7 8; do
  up="$(parent_of "$root")"
  [ -n "$up" ] || break
  root="$up"
done
if ! desc="$(sr-session trajectory describe --path "$root" 2>/dev/null)"; then open; fi
records="$(printf '%s\n' "$root"; printf '%s' "$desc" | jq -r '.subagentPaths[]?' 2>/dev/null)"

# This cycle: what ran since the root's last prompt (a human message, or the
# feedback of a refused Stop). A write made in an earlier cycle was judged at
# that cycle's Stop; a change arriving now that no call of this cycle made —
# a user's own edit between turns — is not the agent's.
entries_of() { sr-session trajectory normalize --path "$1" --events PreCommandInvoke,PreFileCreate,PreFileUpdate 2>/dev/null; }
root_entries="$(entries_of "$root")" || open
since="$(printf '%s' "$root_entries" | jq -r '
  [ .[] | select(.type == "user" and (.isSidechain | not))
    | select(.message.content | if type == "string" then true else (map(.type) | index("tool_result") | not) end)
  ] | last | if . == null then "0\t" else "\(.line)\t\(.timestamp // "")" end' 2>/dev/null)" || open
since_line="$(printf '%s' "$since" | cut -f1)"
since_ts="$(printf '%s' "$since" | cut -f2)"

# Writers this cycle: a call the engine derived a write of the path from
# (named), else a call that could write without naming it (unnamed:
# writers.jq). Git bringing in committed content is neither.
named=""
unnamed=""
while IFS= read -r rec; do
  [ -n "$rec" ] || continue
  [ -r "$rec" ] || open
  if [ "$rec" = "$root" ]; then ents="$root_entries"; else ents="$(entries_of "$rec")" || open; fi
  kinds="$(printf '%s' "$ents" | jq -r -L "$here/../../gate/findings-need-depth" \
      --arg p "$path" --argjson root "$([ "$rec" = "$root" ] && echo true || echo false)" \
      --argjson sl "${since_line:-0}" --arg st "$since_ts" 'include "writers";
    [ .[] | select(if $root then .line > $sl
                   elif $st != "" and (.timestamp // "") != "" then .timestamp >= $st
                   else true end)
      | if names_write($p) then "named" elif runs_unnamed_writer then "unnamed" else empty end ]
    | unique | join(" ")' 2>/dev/null)" || open
  case " $kinds " in *" named "*) named="$named$rec
" ;; esac
  case " $kinds " in *" unnamed "*) unnamed="$unnamed$rec
" ;; esac
done <<EOF
$records
EOF

# A named writer is the writer. With none, the calls that could have written
# it unseen are; with neither, nothing the agent ran this cycle wrote it.
writers="$named"
[ -n "$writers" ] || writers="$unnamed"
[ -n "$writers" ] || exit 1

# Open when this trajectory is a writer or above one.
while IFS= read -r w; do
  [ -n "$w" ] || continue
  cur="$w"
  for _ in 1 2 3 4 5 6 7 8 9; do
    [ "$(rp "$cur")" = "$me" ] && open
    cur="$(parent_of "$cur")"
    [ -n "$cur" ] || break
  done
done <<EOF
$writers
EOF
exit 1
