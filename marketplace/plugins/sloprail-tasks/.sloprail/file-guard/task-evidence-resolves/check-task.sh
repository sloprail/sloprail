#!/usr/bin/env bash
# Refuses a TASK.md whose frontmatter does not satisfy the plugin's task.cue, whose
# artifacts do not resolve, or whose move INTO in_review is not grounded in the
# tool output that proves the work.
#
# THE DETERMINISTIC FLOOR. Three questions, no model:
#   1. SCHEMA — the product's own `sr-file validate` vets the frontmatter against
#      CUE (status is one of five literals, priority one of four, artifacts are
#      repo-relative tree citations, no invented field). --emit hands this script
#      the validated frontmatter as JSON; it parses none of its own.
#   2. ARTIFACTS RESOLVE — every `<repo-relative-file>:<ranges>` names a file under
#      the repo whose cited lines exist; an in_review task names at least one.
#   3. PROOF RIDES ON THE TRANSITION — a write that moves the task INTO in_review
#      (old status is not in_review, new status is) must carry at least one
#      citation whose sourceTypes include `tool_result`: the output of the test run
#      or build that proves the work, cited on the command that makes the claim —
#      `sr-file edit … --cite:tool_result '<exact output>'`. The session resolved
#      it against its own record before this ran, so a citation present EXISTS; a
#      summary in the agent's own words, the user's words, or an answer the user
#      gave to a question never resolves as tool output. The task file carries no
#      transcript path of any kind.
#
# WHAT THIS IS NOT. It does NOT check the BODY's grounding in the user's ask — that
# is task-body-is-human-authored's, against the `user` pool. And it makes NO
# judgement about whether the cited output SUBSTANTIATES the claim — that is
# task-review's model call. See the plugin README for the three-way split.
#
# THE TWO MOMENTS. Preventive: a Pre kind reads the pending bytes and compares with
# `.event.oldContent` (the file on disk); a Post kind at Stop reads the settled
# bytes and compares with `.event.oldContent` (the session baseline) — a create has
# none, so creating a task directly in in_review is a transition too. At Stop the
# event carries every citation the path's changes were made with this session
# (cited changes accumulate; an uncited one leaves them in place), so a
# transition cited at pre-tool is still proven at Stop.
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
# shellcheck source=cite-links.sh
. "$lib"

# WHERE THE BYTES COME FROM depends on the kind. resultKnown is consulted on BOTH
# Pre kinds before newContent is read — an underivable result is deferred to the
# Post kind (exit 0), which the Stop after-check judges (for a preventive guard the
# engine refuses such a write before this script runs).
kind="$(field '.event.kind // ""')"
case "$kind" in
  PostFileCreate | PostFileUpdate)
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

# THE PRIOR STATUS, to tell a move INTO in_review from an edit of a task already
# there. An update carries the prior bytes (disk at Pre, the session baseline at
# Post); a create has none. Prior bytes that do not validate have no status — the
# honest reading is that the task was not in review.
old_status=""
case "$kind" in
  PreFileUpdate | PostFileUpdate)
    old_doc="$(field '.event.oldContent // ""' | sr-file validate - --as .md --schema "$schema" --emit 2>/dev/null)"
    old_status="$(printf '%s' "$old_doc" | jq -r '.status // empty' 2>/dev/null)"
    ;;
esac

n_proof=0
if [ "$old_status" != "in_review" ]; then
  n_proof="$(field '[(.event.citations // [])[] | select(((.sourceTypes // []) | index("tool_result")) != null)] | length')"
fi

missing=""
if [ "${n_art:-0}" -eq 0 ]; then
  missing="${missing}  - artifacts: where the result is — the files the work produced or changed,
    repo-relative, at the lines that changed:  artifacts: [\"src/foo.go:10-60\"]
"
fi
if [ "$old_status" != "in_review" ] && [ "${n_proof:-0}" -eq 0 ]; then
  missing="${missing}  - proof it happened: this write moves the task INTO in_review and cites no tool
    output. Run what proves the work (the tests, the build), then make the status
    change with sr-file, quoting that output exactly, ON ITS OWN in the Bash line
    (nothing else on the line but sr-file calls, &&, and echo):

      sr-file edit $path --old-string 'status: ${old_status:-in_progress}' --new-string 'status: in_review' --cite:tool_result '<exact line of the output>'

    --cite:tool_result is repeatable: cite each result that proves a part of the
    claim. The quote must match exactly one tool result in this session — check it
    with \`sr-session trajectory cite --source-types tool_result '<quote>'\`. Your
    own summary, the user's words, or an answer to a question are not tool output.
"
fi

if [ -n "$missing" ]; then
  case "$kind" in
    Post*) head="EVIDENCE REQUIRED: $path reached in_review this session without its delivery evidence on record: no change to it this session cited tool output." ;;
    *)     head="EVIDENCE REQUIRED: $path is moving to in_review without its delivery evidence." ;;
  esac
  refuse "$head An in_review task claims the work is finished; the reviewer weighs that claim against evidence, so it must carry:

$missing"
fi

exit 0
