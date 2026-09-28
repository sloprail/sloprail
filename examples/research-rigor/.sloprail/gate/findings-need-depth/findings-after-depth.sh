#!/usr/bin/env bash
# Before a research-notes write: if a #research run is open, the write waits for
# depth. Depth itself is judged by depth-check's verify-depth.sh — one rule,
# shared, not a copy that could drift.
set -uo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
input="$(cat)"
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"
path="$(printf '%s' "$input" | jq -r '.event.path // empty')"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath // empty')"
ws="${SR_WORKSPACE:-.}"

block() {
  echo "$1" >&2
  exit 1
}

# Research notes are Markdown in the project, outside a top-level
# dot-directory (the gate's match already keeps absolute and dot paths out).
is_notes() { printf '%s' "$1" | grep -Eiq '[.](md|markdown|mdx)$' && case "$1" in .*) false ;; esac; }

# Whether the write is to NOTES.md itself (or, below, a link to it): there any
# proposal title counts; elsewhere only "Proposed approach" (proposal.jq).
notes_scope=false
printf '%s' "$path" | grep -Eiq '(^|/)notes[.]md$' && notes_scope=true

# Which notes this action would write, if any — the name the refusal uses.
if [ "$kind" = "PreCommandInvoke" ]; then
  # `ln [-s] NOTES.md n.txt` makes a second name for the notes, and a write
  # through that name is not a write of a *.md path: the link itself is held
  # while research is open, on the same line or before the write.
  target="$(printf '%s' "$input" | jq -r '
    [ .event.invocations[]? | select(.bin == "ln")
      | [ .argv[1:][] | select(startswith("-") | not) ] as $ops
      | ($ops | if length >= 2 then .[:-1] else . end)[] as $src
      | {src: $src, link: (if ($ops | length) >= 2 then $ops[-1] else ($src | split("/") | last) end)}
      | select($src | test("(?i)[.](md|markdown|mdx)$"))
      | select(($src | startswith(".") | not) or ($src | startswith("./")))
    ] | first | if . == null then empty else "\(.link) (a link to \(.src))" end' 2>/dev/null)"
  [ -n "$target" ] || exit 0
  path="$target"
elif printf '%s' "$path" | grep -Eiq '[.](txt|text|rst|adoc|asciidoc|org)$'; then
  # Plain-text notes (PROPOSAL.txt, NOTES.rst) are not this project's research
  # notes, but a proposal is a proposal wherever it is written: such a write is
  # held when it adds a "Proposed approach" section, declared research or not.
  adds="$(printf '%s' "$input" | jq -r -L "$here" 'include "proposal";
    if (.event.kind | IN("PreFileCreate", "PreFileUpdate")) and .event.resultKnown == true
    then (.event | adds_proposal(false)) else false end' 2>/dev/null)"
  [ "$adds" = "true" ] || exit 0
  printf '%s' "$input" | DEPTH_FOR_WRITE="$path" DEPTH_PROPOSAL=1 bash "$here/../depth-check/verify-depth.sh"
  exit $?
elif ! is_notes "$path"; then
  # Not a Markdown path — but it may be a second name for one made earlier:
  # a symbolic link resolving to the notes, or a hard link sharing their inode.
  abs="$ws/$path"
  [ -e "$abs" ] || exit 0
  wsreal="$(realpath -q -- "$ws" 2>/dev/null || printf '%s' "$ws")"
  real="$(realpath -q -- "$abs" 2>/dev/null || printf '%s' "$abs")"
  rel="${real#"$wsreal"/}"
  if [ "$rel" != "$real" ] && is_notes "$rel"; then
    printf '%s' "$rel" | grep -Eiq '(^|/)notes[.]md$' && notes_scope=true
    path="$path (a link to $rel)"
  else
    if stat --version >/dev/null 2>&1; then links="$(stat -c %h -- "$abs" 2>/dev/null)"; ino="$(stat -c %i -- "$abs" 2>/dev/null)"
    else links="$(stat -f %l -- "$abs" 2>/dev/null)"; ino="$(stat -f %i -- "$abs" 2>/dev/null)"; fi
    [ "${links:-1}" -gt 1 ] 2>/dev/null || exit 0
    twin="$(find "$ws" -path "$ws/.*" -prune -o -inum "$ino" -type f -print 2>/dev/null \
      | while IFS= read -r f; do r="${f#"$ws"/}"; is_notes "$r" && { printf '%s' "$r"; break; }; done)"
    [ -n "$twin" ] || exit 0
    printf '%s' "$twin" | grep -Eiq '(^|/)notes[.]md$' && notes_scope=true
    path="$path (a hard link to $twin)"
  fi
fi

[ -n "$transcript_path" ] || block "Whether a #research run is open could not be read: the check got no transcript path, so writing $path is held."

# Is a #research run open?
#   - the research-run context is active (declared in an earlier turn, or by a
#     #research dispatch), or
#   - the record already declares #research: a tag in the agent's own text, a
#     sub-agent dispatch whose prompt carries it, or — in a sub-agent — the
#     prompt it was dispatched with. A tag written earlier in THIS turn is on
#     the record before this write's tool call, but the context only hears of
#     it at Stop, which is too late for a write that must not land first.
open="$(printf '%s' "$input" | jq -r '.context["research-run"].active // false')"
if [ "$open" != "true" ]; then
  if ! entries="$(sr-session trajectory normalize --path "$transcript_path" --events PostTagWrite 2>&1)"; then
    block "Whether a #research run is open could not be read from $transcript_path, so writing $path is held: $entries"
  fi
  if ! declared="$(printf '%s' "$entries" | jq -r '
    [ .[]
      | ( (.events[]? | select(.kind == "PostTagWrite") | .tags[]? | select(.label == "research"))
        , (select(.type == "assistant") | .message.content[]? | select(.type == "tool_use")
           | select(.name == "Agent" or .name == "Task")
           | select((.input.prompt // "") | contains("#research")))
        , (select(.type == "user" and .isSidechain == true)
           | .message.content | strings | select(contains("#research"))) )
    ] | length > 0' 2>&1)"; then
    block "Whether a #research run is open could not be decided from $transcript_path, so writing $path is held: $declared"
  fi
  case "$declared" in
    false)
      # No research declared. The write is still held if it IS the proposal —
      # it adds a proposal section (proposal.jq: any proposal title in NOTES.md
      # or a link to it, only "Proposed approach" elsewhere): the convention is
      # research before proposing, declared or not. A write whose result the
      # engine cannot know ahead (resultKnown false) is let through here and
      # judged at Stop by depth-check, which the proposal activates (see the
      # research-run context) — holding every such write would hold unrelated
      # Markdown edits too.
      adds="$(printf '%s' "$input" | jq -r -L "$here" --argjson notes "$notes_scope" 'include "proposal";
        if (.event.kind | IN("PreFileCreate", "PreFileUpdate")) and .event.resultKnown == true
        then (.event | adds_proposal($notes)) else false end' 2>/dev/null)"
      [ "$adds" = "true" ] || exit 0   # an ordinary write
      export DEPTH_PROPOSAL=1
      ;;
    true) ;;
    *) block "Whether a #research run is open could not be decided from $transcript_path (got '$declared'), so writing $path is held." ;;
  esac
fi

# Research is open: the write lands only once the run has depth.
printf '%s' "$input" | DEPTH_FOR_WRITE="$path" bash "$here/../depth-check/verify-depth.sh"
