#!/usr/bin/env bash
# Refuses a TASK.md whose frontmatter does not satisfy the plugin's task.cue, or
# whose artifacts do not resolve or, in in_review, are missing.
#
# THE DETERMINISTIC FLOOR. Three questions, no model:
#   1. SCHEMA — the product's own `sr-file validate` vets the frontmatter against
#      CUE (status is one of five literals, priority one of four, artifacts are
#      repo-relative tree citations, no invented field). --emit hands this script
#      the validated frontmatter as JSON; it parses none of its own.
#   2. ARTIFACTS RESOLVE — every `<repo-relative-file>:<ranges>` names a file under
#      the repo whose cited lines exist; an in_review task names at least one.
#   3. PROOF RIDES ON THE TRANSITION — not this script's: the guard's `require`
#      demands a tool_result citation on a write that moves the task INTO
#      in_review (`when: ./in-review.sh --entering`), and the engine refuses an
#      uncited one before this runs. The task file carries no transcript path of
#      any kind.
#
# WHAT THIS IS NOT. It does NOT check the BODY's grounding in the user's ask — that
# is task-body-is-human-authored's, against the `user` pool. And it makes NO
# judgement about whether the cited output SUBSTANTIATES the claim — that is
# task-review's model call. See the plugin README for the three-way split.
#
# THE TWO MOMENTS. Preventive: a Pre kind reads the pending bytes; a Post kind at
# Stop reads the settled bytes on disk.
#
# THE REFUSAL CONTRACT (internal/dispatch/exec.go): exit 0 permits; any non-zero
# exit refuses, carrying `{"reason": "..."}` on stdout. Fails closed throughout —
# everything it needs (the schema, the filesystem, the event) is local, so a
# failure here is a defect to surface rather than a flaky call to permit past.
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

command -v jq >/dev/null 2>&1 || {
  echo "task-evidence-resolves could not run: it needs jq, which is not on PATH. Refusing, because a check that could not run has not approved the write." >&2
  exit 1
}

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

path="$(field '.event.path // empty')"
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
  refuse "task-evidence-resolves: cite-links.sh not found beside this hook at $lib, so no artifact could be resolved"
fi
# A helper stopped by a syntax error runs only up to it (whether the `.` then
# fails depends on the bash version); only its last-line sentinel proves it
# loaded whole.
unset cite_links_loaded
# shellcheck source=cite-links.sh
. "$lib"
[ "${cite_links_loaded:-}" = 1 ] \
  || refuse "task-evidence-resolves: cite-links.sh did not load whole (its last-line sentinel cite_links_loaded is unset), so no artifact could be resolved"

# WHERE THE BYTES COME FROM depends on the kind. resultKnown is consulted on BOTH
# Pre kinds before newContent is read — an underivable result is deferred to the
# Post kind (exit 0), which the Stop after-check judges (for a preventive guard the
# engine refuses such a write before this script runs).
kind="$(field '.event.kind // ""')"
case "$kind" in
  PostFileCreate | PostFileUpdate)
    # newContentKnown (declared on the Post kinds, internal/filemod/module.go)
    # false: the engine could not read the settled file — a link to a FIFO or a
    # device, or past the read cap. Unseen: refuse rather than pass unchecked.
    [ "$(field '.event.newContentKnown // false')" = "true" ] ||
      refuse "task-evidence-resolves: $path could not be read (not a regular file, or too large), so it could not be checked"
    abs="$root/$path"
    # Written and then removed within the cycle: nothing to check, nothing wrong.
    [ -f "$abs" ] || exit 0
    content="$(cat "$abs")" || refuse "task-evidence-resolves: could not read $path to check it"
    ;;
  PreFileCreate | PreFileUpdate)
    if [ "$(field '.event.resultKnown // false')" != "true" ]; then
      # Not derivable ahead of the write: the settled content is checked at Stop.
      exit 0
    fi
    content="$(field '.event.newContent // ""')"
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
    artifacts:  ["src/foo.go:10-60"]          # repo-relative, in_review only
    depends_on: ["<group>/<task-name>"]       # optional
    ---

There is no `done`. A task the reviewer approves is DELETED, folder and all, by
the reviewer — so an agent marking its own work done is the one move this schema
exists to refuse. There is no transcript path in a task either: the proof that
the work happened is tool output cited on the in_review write
(sr-file … --cite:tool_result '<exact output>'), never a field in the file.
EOF
  refuse "TASK FRONTMATTER INVALID: $path does not satisfy .sloprail/schemas/task.cue.

$detail
$tail"
fi

status="$(printf '%s' "$doc" | jq -r '.status // empty' 2>/dev/null)"

# EVERY ARTIFACT MUST RESOLVE in the working tree, in whatever status it appears. A
# fabricated artifact in an in_progress task is as false as one in a review.
art_lines="$(printf '%s' "$doc" | jq -r '(.artifacts // [])[]' 2>/dev/null)"
n_art="$(printf '%s' "$doc" | jq -r '(.artifacts // []) | length' 2>/dev/null)"
problems=""
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
An ARTIFACT is <repo-relative-file>:<ranges> pointing at a file the work produced
or changed, at the lines that changed, and must exist in the tree. Fix the paths
or the ranges.
EOF
  refuse "EVIDENCE DOES NOT RESOLVE: $path names artifacts that are not there.

$problems
$tail"
fi

[ "$status" = "in_review" ] || exit 0

# The proof that the work happened — tool output cited on the move into
# in_review — is the guard's declared `require`, conditioned on
# `in-review.sh --entering`; it never reaches this script uncited. What is left
# here is where the result is.
if [ "${n_art:-0}" -eq 0 ]; then
  refuse "EVIDENCE REQUIRED: $path is in_review but names no artifacts. An in_review task claims the work is finished; the reviewer weighs that claim against evidence, so it must say where the result is — the files the work produced or changed, repo-relative, at the lines that changed:  artifacts: [\"src/foo.go:10-60\"]"
fi

exit 0
