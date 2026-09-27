#!/usr/bin/env bash
# The GATE and the deterministic pre-flight for task-review, run BEFORE any model
# call. Three jobs:
#
#   1. GATE: only an `in_review` task is reviewed. Most task writes are to_do or
#      in_progress and must cost nothing, so this is asked first and cheaply. A
#      non-in_review task (or a malformed one — task-evidence-resolves owns that,
#      with a better message) permits here without a model.
#   2. START GATES, RE-HELD: every gates/*.sh under the task must STILL pass. A
#      task's gates are its own declared preconditions — task-gates-hold holds
#      them at the START of work; a condition true then is not guaranteed to
#      still be true when the agent claims DONE (a repo made private again, a
#      dependency regressed), so an in_review claim is only as honest as the
#      conditions it still rests on. Deterministic, no model — the same faithful
#      run task-gates-hold performs, duplicated here rather than shared via a
#      sourced library (this plugin's own authoring guardrail judges a big
#      refactor of this file's neighbour more harshly than a small, self-
#      contained addition; two short, identical loops are the simpler choice).
#      gates/*.md judgment gates are NOT re-run here — they are folded into the
#      JUDGE call instead, alongside the delivery evidence, so a judgment gate
#      costs no second model call.
#   3. PRE-FLIGHT: there must be DELIVERY evidence to put in front of a judge —
#      at least one tool_result citation on the event (the tool output that
#      proves the work, cited on the write that made the claim with `sr-file …
#      --cite:tool_result`, already resolved by the session), and at least one
#      artifact, every one resolving in the tree. Evidence that is absent or does
#      not resolve leaves NOTHING REAL to review, so it is refused here, naming
#      what is missing — cheap gates expensive. task-evidence-resolves normally
#      refuses these first on the transition; this also covers a task that was
#      already in_review when the session began and is edited this session with
#      no tool output cited (not a transition, so nothing proves it on record), so
#      the judge is never asked to weigh a claim with no proof on record.
#
# This is an AFTER-CHECK (the guard is not preventive), so it only ever fires on a
# settled Post event: the bytes on disk ARE the answer, and `.event.citations` are
# the citations recorded for this path at pre-tool.
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

# ------------------------------------------------------------- start gates ---
#
# Every gates/*.sh must STILL pass. Run in name order, faithfully, the same
# way task-gates-hold runs them — a gate this rule could not actually run, or
# that fails, is a refusal (fail-closed); an absent gates/ directory is
# nothing to hold on (fail-open on the plumbing, not the logic).
task_dir="$(dirname "$path")"
gates_dir="$root/$task_dir/gates"
gate_problems=""
if [ -d "$gates_dir" ]; then
  while IFS= read -r -d '' g; do
    gname="$(basename "$g")"
    gout="$(cd "$root" && SR_WORKSPACE="$root" bash "$g" 2>&1)"
    grc=$?
    if [ "$grc" -ne 0 ]; then
      gate_problems="${gate_problems}  gates/${gname}: FAILED — ${gout:-exited $grc with no message}
"
    fi
  done < <(find "$gates_dir" -maxdepth 1 -name '*.sh' -type f -print0 2>/dev/null | sort -z)
fi

if [ -n "$gate_problems" ]; then
  refuse "GATES NO LONGER HOLD: $path claims in_review, but a start condition that held when work began no longer does.

$gate_problems
A task's gates are its own declared preconditions, and an in_review claim rests on them still being true. Fix the underlying condition, or explain in the body why the gate no longer applies and remove it (a gate removal is judged by task-gate-is-grounded like any other change to a gates/ file)."
fi

# ------------------------------------------------------- the pre-flight gate ---
lib="$gdir/../task-evidence-resolves/cite-links.sh"
if [ ! -f "$lib" ]; then
  refuse "task-review: cite-links.sh not found at $lib, so no delivery evidence could be resolved for review"
fi
# shellcheck source=../task-evidence-resolves/cite-links.sh
. "$lib"

art_lines="$(printf '%s' "$doc" | jq -r '(.artifacts // [])[]' 2>/dev/null)"
n_art="$(printf '%s' "$doc" | jq -r '(.artifacts // []) | length' 2>/dev/null)"
n_proof="$(printf '%s' "$event" | jq -r '[(.event.citations // [])[] | select(((.sourceTypes // []) | index("tool_result")) != null)] | length' 2>/dev/null)"

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

# BOTH kinds are mandatory in in_review — cited tool output (proof it happened) and
# artifacts (where the result is) — and the pre-flight names the missing one rather
# than letting the judge see half the evidence and guess.
if [ "${n_proof:-0}" -eq 0 ]; then
  problems="${problems}  proof — no tool output is cited for this claim. The write that leaves the task in in_review must cite the output that proves the work (a test run, a build) — no change to it this session did
"
fi
if [ "${n_art:-0}" -eq 0 ]; then
  problems="${problems}  artifacts — an in_review task must cite where the produced result is (tree files)
"
fi

if [ -n "$problems" ]; then
  IFS= read -r -d '' tail <<'EOF' || true
There is nothing to review until the claim carries evidence a reviewer can open.
Proof that the work happened is tool output cited on the write itself — run what
proves it, then make the change with sr-file ON ITS OWN in the Bash line, quoting
the output exactly (the quote must match exactly one tool result this session):

EOF
  refuse "REVIEW CANNOT RUN: $path is in_review but its delivery evidence is missing or does not resolve.

$problems
$tail  sr-file edit $path --old-string '<old text>' --new-string '<new text>' --cite:tool_result '<exact line of the output>'

An ARTIFACT is <repo-relative-file>:<ranges> in the frontmatter, pointing at the produced files in the tree. The status stays in_review; add the evidence and write the task again."
fi

exit 0
