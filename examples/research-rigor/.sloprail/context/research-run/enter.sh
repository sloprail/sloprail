#!/usr/bin/env bash
# enter: a #research tag or dispatch (the triggers' match already confirmed
# it) opens a declared run. A file write opens one only if it added a
# "Proposed approach" section — the findings — and was not already seen at an
# earlier Stop; an active run stays as it is. See context.md — a clean exit
# ACTIVATES, so every "no" here exits non-zero.
set -uo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
input="$(cat)"
kind="$(printf '%s' "$input" | jq -r '.event.kind // empty')"

case "$kind" in
  PostFileCreate | PostFileUpdate)
    # Declining is a NON-ZERO exit: it leaves the context exactly as it was.
    # (A clean exit with no output would activate it, keeping the old payload.)
    # Already open: keep what opened it — a declared run stays declared.
    [ "$(printf '%s' "$input" | jq -r '.currentContext.active // false')" != "true" ] || exit 1
    adds="$(printf '%s' "$input" | jq -r -L "$here/../../gate/findings-need-depth" 'include "proposal";
      if .event.seen == true then false else (.event | adds_proposal) end' 2>/dev/null)"
    [ "$adds" = "true" ] || exit 1
    # Only the trajectory that WROTE the proposal owes the research for it. A
    # Stop sees every file that changed during the session, so a sub-agent
    # whose dispatcher wrote NOTES.md meanwhile would otherwise be refused for
    # a proposal it never made. The writer is a tool call on this record that
    # wrote the path: the Write/Edit tools, or a shell line that writes into it
    # (redirect, tee, sed -i, cp/mv/rsync/ln, an interpreter). An unreadable
    # record is not evidence against it: open (fail closed).
    path="$(printf '%s' "$input" | jq -r '.event.path // empty')"
    tp="$(printf '%s' "$input" | jq -r '.transcriptPath // empty')"
    if [ -n "$tp" ] && [ -r "$tp" ]; then
      wrote="$(jq -r --arg p "$path" '
        ($p | ascii_downcase) as $lp
        | select(.type == "assistant") | .message.content[]? | select(.type == "tool_use")
        | select(
            ((.name | IN("Write", "Edit", "MultiEdit")) and ((.input.file_path // "") | ascii_downcase | . == $lp or endswith("/" + $lp)))
            or (.name == "Bash" and ((.input.command // "") | ascii_downcase
                | (index($lp) != null) and test("(>>?|\\btee\\b|\\bsed\\s+-i|\\bcp\\b|\\bmv\\b|\\brsync\\b|\\bln\\b|\\bpython|\\bnode\\b|\\bperl\\b|\\bruby\\b|\\beval\\b)"))))
        | "yes"' "$tp" 2>/dev/null | head -n 1)"
      [ "$wrote" = "yes" ] || exit 1
    fi
    printf '%s' "$input" | jq -c '{declared: false, proposal: .event.path}'
    ;;
  *)
    jq -n '{declared: true}'
    ;;
esac
