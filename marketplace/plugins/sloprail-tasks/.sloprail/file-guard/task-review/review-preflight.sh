#!/usr/bin/env bash
# The GATE and the deterministic pre-flight for task-review, run BEFORE any model
# call. Two jobs:
#
#   1. GATE: only an `in_review` task is reviewed. Most task writes are to_do or
#      in_progress and must cost nothing, so this is asked first and cheaply. A
#      non-in_review task (or a malformed one — task-evidence-resolves owns that,
#      with a better message) permits here without a model.
#   2. PRE-FLIGHT: the DELIVERY evidence must resolve — every observation must
#      ground against the tool_result pool, and every artifact must exist in the
#      tree. Evidence that does not resolve leaves NOTHING REAL to put in front of a
#      judge, so it is refused here, naming what failed — cheap gates expensive.
#      task-evidence-resolves normally refuses these first; this handles the case it
#      was bypassed, so an in_review task never reaches the judge with broken
#      evidence.
#
# This is an AFTER-CHECK (the guard is not preventive), so it only ever fires on a
# settled Post event: the bytes on disk ARE the answer.
#
# THE REFUSAL CONTRACT: exit 0 permits; non-zero refuses with `{"reason": "..."}`
# on stdout. Fails closed on the deterministic logic; the schema-read path stays out
# of the way for a malformed task (which the sibling rule already refuses).
set -uo pipefail

# refuse emits `{"reason": ...}` and exits non-zero, in the CURRENT shell — never
# behind a pipe (a piped refuse runs in a subshell and its exit would not stop the
# script, silently PERMITTING). Callers build the reason into a variable first: a
# STATIC tail via a QUOTED heredoc joined to a dynamic head in a double-quoted
# string, whose `$var` expansion is not recursive, so a `$(...)` in evidence text is
# inert data.
refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  refuse "task-review: the event named no path, so there is nothing to review"
fi

root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"
abs="$root/$path"

# Post kinds only, so the bytes on disk are what actually landed. Written and then
# removed inside one cycle: nothing to review, nothing wrong.
[ -f "$abs" ] || exit 0
[ -s "$abs" ] || exit 0

# ---------------------------------------------------------------- the gate ---
#
# Only `in_review` is reviewed. The status comes from the product's own command,
# validated against the schema, never a hand-rolled frontmatter read that would be a
# second opinion about where frontmatter ends. --emit prints the validated
# frontmatter as JSON on success and NOTHING on failure — so a malformed task comes
# back with an empty status and this rule stays out of the way (correct:
# task-evidence-resolves is already refusing that write with a better message).
# The schema is the PLUGIN's, read from the plugin's own tree — see
# task-evidence-resolves/check-task.sh for why it is never a consumer-side copy.
schema="$gdir/../../schemas/task.cue"
if [ ! -f "$schema" ]; then
  # Without the schema the status cannot be read. This is the guard's own dependency
  # missing, not a model flake — refuse, naming the fix.
  refuse "task-review: schema not found at $schema, so the task's status cannot be read."
fi

doc="$(sr-file validate "$abs" --schema "$schema" --emit 2>/dev/null)"
status="$(printf '%s' "$doc" | jq -r '.status // empty' 2>/dev/null)"
[ "$status" = "in_review" ] || exit 0

# ------------------------------------------------------- the pre-flight gate ---
lib="$gdir/../task-evidence-resolves/cite-links.sh"
if [ ! -f "$lib" ]; then
  refuse "task-review: cite-links.sh not found at $lib, so no delivery evidence could be resolved for review"
fi
# shellcheck source=../task-evidence-resolves/cite-links.sh
. "$lib"

obs_lines="$(printf '%s' "$doc" | jq -r '(.observations // [])[]' 2>/dev/null)"
art_lines="$(printf '%s' "$doc" | jq -r '(.artifacts // [])[]' 2>/dev/null)"
n_obs="$(printf '%s' "$doc" | jq -r '(.observations // []) | length' 2>/dev/null)"
n_art="$(printf '%s' "$doc" | jq -r '(.artifacts // []) | length' 2>/dev/null)"

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

# BOTH kinds are mandatory in in_review, and the pre-flight names the missing one
# rather than letting the judge see half the evidence and guess.
if [ "${n_obs:-0}" -eq 0 ]; then
  problems="${problems}  observations — an in_review task must cite proof the work happened (a tool-call result)
"
fi
if [ "${n_art:-0}" -eq 0 ]; then
  problems="${problems}  artifacts — an in_review task must cite where the produced result is (tree files)
"
fi

if [ -n "$problems" ]; then
  IFS= read -r -d '' tail <<'EOF' || true
There is nothing to review until every citation resolves to bytes a reviewer can
open. An OBSERVATION is <absolute-session.jsonl>:<ranges> whose every cited line is a
tool-call result; an ARTIFACT is <repo-relative-file>:<ranges> pointing at the
produced files in the tree. Fix the paths or the ranges. The status stays in_review;
correct the evidence and write the task again.
EOF
  refuse "REVIEW CANNOT RUN: $path is in_review but its delivery evidence does not resolve.

$problems
$tail"
fi

exit 0
