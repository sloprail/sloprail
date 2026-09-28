#!/usr/bin/env bash
# prepare: ask the judge only about a write that changes what a marker pins — the
# same decision changes-pinned-lines.sh makes for the citation requirement, drawn
# at the same line the engine's `when` draws it: only a decided waiver skips the
# judge — exit 1 WITH the predicate's `{"waived": …}` sentinel. Any other outcome
# (exit 0, a crash, a script that could not run, an exit 1 that printed no
# sentinel) leaves the citation demanded, and so goes to the judge; skipping it
# there would let any resolvable quote admit the change.
#
# The predicate's `what` says what the change does to which pin; the judge gets it.
set -uo pipefail

out="$("${SR_GUARDRAIL_DIR:-.}/changes-pinned-lines.sh")"
rc=$?
if [ "$rc" -eq 1 ] && printf '%s' "$out" | jq -e 'has("waived")' >/dev/null 2>&1; then
  printf '{"skip": true}\n'
  exit 0
fi

what="$(printf '%s' "$out" | jq -r '.what // .hint // empty' 2>/dev/null)"
[ -n "$what" ] || what="Whether this change touches a pinned rule could not be decided (the check deciding it exited $rc). Judge it as a change to a pinned rule."
# The template reads additionalContext.what, and an undefined key fails the render
# closed, so even the fallback carries it.
jq -n --arg what "$what" '{additionalContext: {what: $what}}' 2>/dev/null \
  || printf '{"additionalContext": {"what": "What this change does to a pinned rule could not be described. Judge it as a change to a pinned rule."}}\n'
