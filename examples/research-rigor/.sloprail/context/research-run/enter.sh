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
# sibling or one below. A write no record names — an interpreter assembling
# the path, a script run from a file — belongs to the session's root. Anything
# that cannot be read opens the run (fail closed).
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

# Writers: records with a tool call that names the path as something it
# writes — the Write/Edit tools, or a shell line naming it alongside a writer
# (redirect, tee, sed -i, cp/mv/rsync/ln, dd, patch, git apply, an
# interpreter, eval). One malformed line does not hide the rest.
writers=""
while IFS= read -r rec; do
  [ -n "$rec" ] || continue
  [ -r "$rec" ] || open
  hit="$(jq -R -r --arg p "$path" '
    fromjson? | select(.type == "assistant") | .message.content[]? | select(.type == "tool_use")
    | select(
        ((.name | IN("Write", "Edit", "MultiEdit")) and ((.input.file_path // "") | ascii_downcase | . == $p or endswith("/" + $p)))
        or (.name == "Bash" and ((.input.command // "") | ascii_downcase
            | (index($p) != null)
              and test("(>>?|\\btee\\b|\\bsed\\s+-i|\\bcp\\b|\\bmv\\b|\\brsync\\b|\\bln\\b|\\bdd\\b|\\bpatch\\b|\\bgit\\s+apply\\b|\\bpython|\\bnode\\b|\\bperl\\b|\\bruby\\b|\\beval\\b)"))))
    | "yes"' "$rec" 2>/dev/null | head -n 1)"
  [ "$hit" = "yes" ] && writers="$writers$rec
"
done <<EOF
$records
EOF

# No record names it: the session's root owes it.
[ -n "$writers" ] || writers="$root
"

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
