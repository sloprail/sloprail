#!/usr/bin/env bash
# Refuses a TASK.md whose frontmatter does not satisfy .sloprail/schemas/task.cue,
# or whose evidence citations do not resolve to the user's own words.
#
# Two deterministic questions, no model:
#   1. SCHEMA — the product's own `sr-file validate` vets the frontmatter against
#      CUE (status is one of five literals, priority one of four, no invented
#      field). This script parses no frontmatter of its own.
#   2. EVIDENCE — every `[quote](jsonl-path)` citation LINK in the content must
#      ground: `sr-session trajectory cite --path <jsonl-path> "<quote>"` must
#      resolve the quote to a real user line. cite is the SINGLE validator — it
#      already excludes tool results AND harness-injected user-role messages
#      (<system-reminder>/<task-notification>/<local-command>/…), so this script
#      walks no transcript and filters no tags. And an `in_review` task must carry
#      at least one such grounded link — a claim of finished work with no evidence
#      is exactly what this rule refuses.
#
# THE REFUSAL CONTRACT (internal/dispatch/exec.go): exit 0 permits; any non-zero
# exit refuses, carrying `{"reason": "..."}` on stdout as what the agent is shown.
# Fails closed throughout — everything it needs (the schema, the filesystem, cite)
# is local, so a failure here is a defect to surface rather than a flaky model call
# to permit past.
#
# `set -uo pipefail`, never `set -e`.
set -uo pipefail

# refuse emits the structured `{"reason": ...}` and exits non-zero, in the CURRENT
# shell (never behind a pipe — `something | refuse` would run refuse in a subshell
# and its exit would not stop the script, silently PERMITTING). Callers build the
# reason into a variable first, with the reason() helper below, and pass it as $1.
refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}


event="$(cat)"

# Flat event fields (new-format CheckPayload): `.event.path`, `.event.kind`,
# `.event.newContent`, `.event.resultKnown` — read directly, not through the old
# nested envelope.
path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  refuse "task-evidence-resolves: the event named no path, so there is nothing to check"
fi

# $SR_WORKSPACE is the project root the engine names; $SR_GUARDRAIL_DIR is this
# guard's own folder, so cite-links.sh resolves beside this script. Both are set
# by the engine (internal/dispatch/exec.go) — never guessed from $PWD.
root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"

schema="$root/.sloprail/schemas/task.cue"
if [ ! -f "$schema" ]; then
  refuse "task-evidence-resolves: schema not found at $schema — the rule cannot check anything without it. Install the plugin's task.cue under the project's .sloprail/schemas/."
fi

lib="$gdir/cite-links.sh"
if [ ! -f "$lib" ]; then
  refuse "task-evidence-resolves: cite-links.sh not found beside this hook at $lib, so no citation could be resolved"
fi
# shellcheck source=cite-links.sh
. "$lib"

# The trajectory the guard's transcript-grounding falls back to when a citation
# link names only `:line` on a bare path — carried on the CheckPayload. Not used
# to override an explicit href; cite is given each link's own href.
transcript_path="$(printf '%s' "$event" | jq -r '.transcriptPath // ""' 2>/dev/null)"

# WHERE THE BYTES COME FROM depends on the kind. A Pre event carries the pending
# newContent; a Post event's file on disk is what actually landed. resultKnown is
# consulted on BOTH Pre kinds before newContent is read — an underivable result is
# deferred to the Post kind (exit 0), which the Stop after-check judges. This is
# why the guard is preventive-with-after-check rather than Pre-only.
kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    abs="$root/$path"
    # Written and then removed within the cycle: nothing to check, nothing wrong.
    [ -f "$abs" ] || exit 0
    content="$(cat "$abs" 2>/dev/null)" || exit 0
    ;;
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$event" | jq -r '.event.resultKnown // false' 2>/dev/null)"
    if [ "$known" != "true" ]; then
      # Not derivable ahead of the write: the settled content is checked at Stop.
      exit 0
    fi
    content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    ;;
  *)
    # A delete, or a kind this guard is not about: nothing to check.
    exit 0
    ;;
esac

# THE SCHEMA. --emit prints the validated frontmatter as JSON on success, which is
# what lets the status be read below without parsing these bytes a second time.
if ! doc="$(printf '%s' "$content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)"; then
  detail="$(printf '%s' "$doc" | sed "s|^-:|$path:|g")"
  # The static tail is a QUOTED heredoc (no expansion); the dynamic head is joined
  # by an ordinary double-quoted string. $detail is DATA in a quoted expansion, so
  # a `$(...)` or backtick in the validator output cannot execute.
  IFS= read -r -d '' tail <<'EOF' || true

A task's frontmatter is exactly:

    ---
    status: backlog | to_do | in_progress | in_review | blocked
    priority: P0 | P1 | P2 | P3
    ---

There is no `done`. A task the reviewer approves is DELETED, folder and all, by
the reviewer — so an agent marking its own work done is the one move this schema
exists to refuse.
EOF
  refuse "TASK FRONTMATTER INVALID: $path does not satisfy .sloprail/schemas/task.cue.

$detail
$tail"
fi

status="$(printf '%s' "$doc" | jq -r '.status // empty' 2>/dev/null)"

# EVERY EVIDENCE LINK MUST GROUND, in whatever status it appears — a fabricated
# citation in a to_do task is as false as one in a review. cite is the authority.
problems=""
n_links=0
while IFS="$(printf '\t')" read -r href quote; do
  [ -n "$href" ] || continue
  n_links=$((n_links + 1))
  cpath="$(cite_link_href_path "$href")"
  case "$cpath" in
    /*) : ;;                       # absolute — use as-is
    *)  cpath="$root/$cpath" ;;    # relative — resolve under the workspace
  esac
  if reason="$(cite_ground "$cpath" "$quote")"; then :; else
    problems="${problems}  ${reason}
"
  fi
done <<EOF
$(cite_links_extract "$content")
EOF

if [ -n "$problems" ]; then
  IFS= read -r -d '' tail <<'EOF' || true
A citation is a markdown link [<quote>](<jsonl-path>): the link text is the user's
own words, verbatim, and the href is the transcript .jsonl they said them in
(optionally :line). It has to resolve — via sr-session trajectory cite — to a real
user message. Quote what the user actually said and point at the transcript.
EOF
  refuse "CITATION DOES NOT RESOLVE: $path names evidence that is not the user's words.

$problems
$tail"
fi

# THE EVIDENCE IS MANDATORY IN in_review: a claim of finished work must carry at
# least one grounded citation. The schema marks the evidence optional (it cannot
# express the conditional without turning the failure into a unification conflict),
# so the requirement is enforced here, where the refusal can say what to attach.
if [ "$status" = "in_review" ] && [ "$n_links" -eq 0 ]; then
  IFS= read -r -d '' tail <<'EOF' || true

An in_review task claims the work is finished. It must ground that claim in the
user's own words about what was asked and what proves it done, as one or more
markdown links:

    [the user's exact words](/abs/session.jsonl:120)

Each link's quote must resolve — via cite — to a real user message. Attach the
evidence and retry.
EOF
  refuse "EVIDENCE REQUIRED: $path is in_review but carries no citation of the work.
$tail"
fi

exit 0
