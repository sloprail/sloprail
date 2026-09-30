#!/bin/sh
# Refuses a guardrail HOOK SCRIPT that carries a shape measured to make a rule
# silently inert. This is a file-guard's check (new format): it receives a
# CheckPayload on stdin and its cwd is the guard's own folder, so rules/ and the
# RULE.md files resolve beside it.
#
# Only the shapes recorded as `enforced: true` in rules/<name>/RULE.md are
# checked here — each is one deterministic grep below. A rule that needs
# judgement is documented and left un-enforced (its RULE.md carries no grep): a
# check that fires on taste gets switched off, and then the decidable ones go
# with it.
#
# Exit 0 permits, non-zero refuses.

lib_dir="$(cd "$(dirname "$0")" && pwd)"
unset check_rules_lib_loaded
. "$lib_dir/check-rules-lib.sh" || exit 2
[ "${check_rules_lib_loaded:-}" = 1 ] || exit 2
lib_init
# This file-guard checks the SETTLED file at Stop (the PreFileWrite gate of the
# same name, beside it under gate/, refuses a slop hook before it lands). The
# event's kind is a Post kind, whose bytes were read by the engine the one safe
# way (a regular file, capped): `newContentKnown` says whether it could.
#
# All fields read FLAT under `.event`, the CheckPayload shape.
kind="$(printf '%s' "$event" | jq -r '.event.kind // empty' 2>/dev/null)"
[ -n "$kind" ] || { echo "authoring-slop: could not read the event's kind, so it could not be checked" >&2; exit 2; }
if [ "$kind" = "PostFileCreate" ] || [ "$kind" = "PostFileUpdate" ]; then
  # The engine could not read the settled bytes (newContentKnown false): a
  # script this check cannot see is refused, not permitted unread.
  if [ "$(printf '%s' "$event" | jq -r 'if (.event | has("newContentKnown")) then .event.newContentKnown else true end' 2>/dev/null)" != "true" ]; then
    echo "authoring-slop: the engine could not read the settled $path (newContentKnown false: not a regular file, or too large), so it could not be checked. Make it an ordinary script file." >&2
    exit 1
  fi
fi
body="$(printf '%s' "$event" | jq -r '.event.newContent // empty' 2>/dev/null)"
if [ -z "$body" ]; then
  abs="${SR_WORKSPACE:-.}/$path"
  [ -f "$abs" ] || exit 0
  body="$(cat "$abs")" || {
    echo "authoring-slop: could not read the settled $path, so it could not be checked." >&2
    exit 1
  }
fi
lib_check
