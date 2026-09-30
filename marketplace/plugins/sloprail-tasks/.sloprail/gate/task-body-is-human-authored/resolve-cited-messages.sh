#!/usr/bin/env bash
# prepare for stage 2 of task-body-is-human-authored: hand the judge the body it
# rules on. The cited words need no preparing: judge-body.md.j2 reads them
# straight off `.event.citations` (each quote with the whole message it came
# from). Receives the SAME CheckPayload stage 1 (body-is-stated.sh) did.
#
# SKIPS THE JUDGE when no grounding was required — a status/frontmatter-only change
# leaves the body byte-identical, and body-changed.sh waived the citation — so
# no model call is spent on a write that changed nothing the judge rules on. This is the gate's copy (pending bytes); the file-guard's carries the Stop copy.
#
# Output nests under `additionalContext` (the one key the engine reads from a
# prepare): .body (the prose the judge rules on) and .asks (the event's citations
# in the user pool). `{"skip": true}` abstains. A non-zero exit fails the check closed.
set -uo pipefail

skip() { printf '{"skip": true}\n'; exit 0; }

command -v jq >/dev/null 2>&1 || {
  echo "task-body-is-human-authored: jq is not on PATH, so the body could not be read" >&2
  exit 1
}

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

path="$(field '.event.path // empty')"
kind="$(field '.event.kind // ""')"

lib="${SR_GUARDRAIL_DIR:-.}/lib-body.sh"
if [ ! -f "$lib" ]; then
  echo "task-body-is-human-authored: lib-body.sh not found at $lib, so the cited messages could not be assembled" >&2
  exit 1
fi
# A helper stopped by a syntax error runs only up to it (whether the `.` then
# fails depends on the bash version); only its last-line sentinel proves it
# loaded whole.
unset lib_body_loaded
# shellcheck source=lib-body.sh
. "$lib"
if [ "${lib_body_loaded:-}" != 1 ]; then
  echo "task-body-is-human-authored: lib-body.sh did not load whole (its last-line sentinel lib_body_loaded is unset), so the cited messages could not be assembled" >&2
  exit 1
fi

# The same kind dispatch as stage 1, so the two never disagree about which bytes
# are the body. Stage 1 already refused an unknown result; this fails closed too
# rather than skipping, should it ever be reached with one.
case "$kind" in
  PreFileCreate | PreFileUpdate)
    if [ "$(field '.event.resultKnown // false')" != "true" ]; then
      echo "task-body-is-human-authored: the result of this write to $path could not be computed ahead of the write, so its body could not be judged" >&2
      exit 1
    fi
    content="$(field '.event.newContent // ""')"
    ;;
  *)
    skip
    ;;
esac

body="$(task_body "$content")"

# Grounding not required: the body is unchanged, so there is nothing to judge.
case "$kind" in
  PreFileUpdate)
    [ "$body" = "$(task_body "$(field '.event.oldContent // ""')")" ] && skip
    ;;
esac

# .asks is the event's citations in the user pool only: a path's citations are
# those of every cited change that landed on it, and the tool output a later
# in_review move cited is not the ask, so the judge is never shown it as if it
# were.
printf '%s' "$payload" | jq --arg body "$body" \
  '{additionalContext: {body: $body, asks: [(.event.citations // [])[] | select(((.sourceTypes // []) | index("user")) != null)]}}'
