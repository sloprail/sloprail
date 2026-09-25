#!/usr/bin/env bash
# Refuses a TASK.md whose frontmatter does not satisfy .sloprail/schemas/task.cue,
# or whose DELIVERY-evidence citations do not resolve.
#
# THE DETERMINISTIC FLOOR. Two deterministic questions, no model:
#   1. SCHEMA — the product's own `sr-file validate` vets the frontmatter against
#      CUE (status is one of five literals, priority one of four, observations are
#      transcript links, artifacts are tree file citations, no invented field).
#      This script parses no frontmatter of its own; --emit hands it the validated
#      lists as JSON.
#   2. EVIDENCE RESOLVES — every observation and every artifact in the frontmatter
#      must resolve to the thing its KIND promises. The two are FRONTMATTER citation
#      strings with DIFFERENT PATH BASES:
#        - an OBSERVATION `<abs-jsonl>:<ranges>` cites the session transcript by
#          ABSOLUTE path; every cited LINE must be a tool_result the session produced
#          (via `sr-session trajectory tool-result --line`), not the user's words and
#          not the agent's narration.
#        - an ARTIFACT `<repo-relative-file>:<ranges>` cites a produced file in the
#          WORKING TREE by REPO-RELATIVE path; the file exists under the repo and
#          every cited line exists.
#      The path base is what routes each to its resolver — an observation is never
#      sent through the tree checker, an artifact never through the transcript reader.
#      And an `in_review` task must carry AT LEAST ONE of each — a claim of finished
#      work with no proof it happened and no result to review is exactly what this
#      refuses.
#
# WHAT THIS IS NOT. It does NOT check the BODY's citation of the ASK — that is
# task-body-is-human-authored's subject, grounded against the `user` pool. And it
# makes NO judgement about whether the evidence SUBSTANTIATES the claim — that is
# task-review's model call. This rule answers only "do the citations resolve",
# which is the deterministic gate both of those depend on. See the plugin README
# for the three-way split (body / this / review).
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
# reason into a variable first, with a QUOTED heredoc tail joined to a dynamic head.
refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

event="$(cat)"

# Flat event fields (new-format CheckPayload): `.event.path`, `.event.kind`,
# `.event.newContent`, `.event.resultKnown` — read directly.
path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  refuse "task-evidence-resolves: the event named no path, so there is nothing to check"
fi

# $SR_WORKSPACE is the project root the engine names; $SR_GUARDRAIL_DIR is this
# guard's own folder, so cite-links.sh resolves beside this script. Both are set by
# the engine (internal/dispatch/exec.go) — never guessed from $PWD.
root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"

# The schema is the PLUGIN's, read from the plugin's own tree — never copied
# into a consumer's .sloprail/schemas/. $gdir is this guard's own folder, two
# levels under the plugin's .sloprail/, so ../../schemas/task.cue is the
# plugin's schemas/.
schema="$gdir/../../schemas/task.cue"
if [ ! -f "$schema" ]; then
  refuse "task-evidence-resolves: schema not found at $schema — the rule cannot check anything without it."
fi

lib="$gdir/cite-links.sh"
if [ ! -f "$lib" ]; then
  refuse "task-evidence-resolves: cite-links.sh not found beside this hook at $lib, so no citation could be resolved"
fi
# shellcheck source=cite-links.sh
. "$lib"

# WHERE THE BYTES COME FROM depends on the kind. A Pre event carries the pending
# newContent; a Post event's file on disk is what actually landed. resultKnown is
# consulted on BOTH Pre kinds before newContent is read — an underivable result is
# deferred to the Post kind (exit 0), which the Stop after-check judges. This is why
# the guard is preventive-with-after-check rather than Pre-only.
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
# what lets the lists be read below without parsing these bytes a second time.
if ! doc="$(printf '%s' "$content" | sr-file validate - --as .md --schema "$schema" --emit 2>&1)"; then
  detail="$(printf '%s' "$doc" | sed "s|^-:|$path:|g")"
  # The static tail is a QUOTED heredoc (no expansion); the dynamic head is joined
  # by an ordinary double-quoted string. $detail is DATA in a quoted expansion, so a
  # `$(...)` or backtick in the validator output cannot execute.
  IFS= read -r -d '' tail <<'EOF' || true

A task's frontmatter is exactly:

    ---
    status: backlog | to_do | in_progress | in_review | blocked
    priority: P0 | P1 | P2 | P3
    observations: ["/abs/session.jsonl:120"]   # absolute .jsonl, in_review only
    artifacts:    ["src/foo.go:10-60"]          # repo-relative, in_review only
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

# THE EVIDENCE, read out of the VALIDATED frontmatter — one citation per line, so a
# path with a space is not torn in half by a word-split. `-c … | .[]` on jq.
obs_lines="$(printf '%s' "$doc" | jq -r '(.observations // [])[]' 2>/dev/null)"
art_lines="$(printf '%s' "$doc" | jq -r '(.artifacts // [])[]' 2>/dev/null)"

n_obs="$(printf '%s' "$doc" | jq -r '(.observations // []) | length' 2>/dev/null)"
n_art="$(printf '%s' "$doc" | jq -r '(.artifacts // []) | length' 2>/dev/null)"

# EVERY OBSERVATION MUST RESOLVE — every cited transcript line is a tool_result — in
# whatever status it appears. A fabricated delivery citation in an in_progress task
# is as false as one in a review.
problems=""
i=0
while IFS= read -r obs; do
  [ -n "$obs" ] || continue
  if reason="$(observation_resolve "$obs" "$root")"; then :; else
    problems="${problems}  observations[$i] ${reason}
"
  fi
  i=$((i + 1))
done <<EOF
$obs_lines
EOF

# EVERY ARTIFACT MUST RESOLVE in the working tree.
i=0
while IFS= read -r art; do
  [ -n "$art" ] || continue
  if reason="$(artifact_resolve "$art" "$root")"; then :; else
    problems="${problems}  artifacts[$i] ${reason}
"
  fi
  i=$((i + 1))
done <<EOF
$art_lines
EOF

if [ -n "$problems" ]; then
  IFS= read -r -d '' tail <<'EOF' || true
An OBSERVATION is <absolute-session.jsonl>:<ranges> and every cited line must be a
tool-call RESULT the session produced — a test that came back green, a command
whose output is the evidence. An ARTIFACT is <repo-relative-file>:<ranges> pointing
at the files the work produced, at the lines that changed, and must exist in the
tree. An observation is an ABSOLUTE .jsonl; an artifact is a REPO-RELATIVE tree
path — do not swap them. Fix the paths or the ranges.
EOF
  refuse "EVIDENCE DOES NOT RESOLVE: $path names delivery evidence that is not there.

$problems
$tail"
fi

# THE EVIDENCE IS MANDATORY IN in_review: a claim of finished work must carry both a
# proof it happened (an observation) and where the result is (an artifact). The
# schema marks both optional (it cannot express the conditional without turning the
# failure into a unification conflict), so the requirement is enforced here, where
# the refusal can say what to attach and why.
if [ "$status" = "in_review" ] && { [ "${n_obs:-0}" -eq 0 ] || [ "${n_art:-0}" -eq 0 ]; }; then
  IFS= read -r -d '' tail <<'EOF' || true

An in_review task claims the work is finished, and must carry BOTH kinds of
delivery evidence:

    observations: ["/abs/session.jsonl:120"]   proof it happened (absolute .jsonl)
    artifacts:    ["src/foo.go:10-60"]          where the result is (repo-relative)

observations cite the session transcript by absolute path — the test run that came
back green, the command whose output is the evidence (every cited line is a
tool_result, not a summary saying it went well). artifacts cite the files the work
produced or changed, repo-relative, at the lines that changed, so the reviewer reads
the result. Attach both and retry.
EOF
  refuse "EVIDENCE REQUIRED: $path is in_review but is missing its delivery evidence.
$tail"
fi

exit 0
