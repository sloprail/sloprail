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

lib_dir="$(cd "$(dirname "$0")/../../file-guard/authoring-slop" && pwd)"
unset check_rules_lib_loaded
. "$lib_dir/check-rules-lib.sh" || exit 2
[ "${check_rules_lib_loaded:-}" = 1 ] || exit 2
lib_init
# This is the PRE-WRITE gate (the plain file-guard of the same name, beside it
# under file-guard/, is the check of the settled file at Stop). On either Pre kind
# `newContent` is present only when the result is derivable — `resultKnown` is the
# flag that says so. This is symmetric across create and update: an underivable
# PreFileUpdate (a command-derived edit) and an underivable PreFileCreate (a
# NotebookEdit fresh-.ipynb, whose cell source is not the JSON document) BOTH carry
# newContent "" with resultKnown false. A gate that cannot see the bytes it is
# about to admit has not checked them, so it REFUSES rather than passing: the agent
# is told to write the script directly.
#
# All fields read FLAT under `.event`, the CheckPayload shape.
kind="$(printf '%s' "$event" | jq -r '.event.kind // empty' 2>/dev/null)"
[ -n "$kind" ] || { echo "authoring-slop: could not read the event's kind, so it could not be checked" >&2; exit 2; }
if [ "$(printf '%s' "$event" | jq -r '.event.resultKnown // false' 2>/dev/null)" != "true" ]; then
  echo "authoring-slop: the engine could not compute what this $kind would write to $path (an in-place or environment-dependent edit, or a notebook create), so it could not be checked before it lands. Refusing: a check that could not run has not approved. Write the file's content directly." >&2
  exit 1
fi
body="$(printf '%s' "$event" | jq -r '.event.newContent // empty' 2>/dev/null)"
if [ -z "$body" ]; then
  # A known, genuinely-empty script has nothing to flag.
  exit 0
fi
lib_check
