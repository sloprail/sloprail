#!/usr/bin/env bash
# The GATE and the deterministic pre-flight for task-review, run BEFORE any model
# call. Two jobs:
#
#   1. GATE: only an `in_review` task is reviewed. Most task writes are to_do or
#      in_progress and must cost nothing, so this is asked first and cheaply. A
#      non-in_review task (or a malformed one — task-evidence-resolves owns that,
#      with a better message) permits here without a model.
#   2. PRE-FLIGHT: every `[quote](jsonl)` evidence link must GROUND via cite. An
#      ungrounded citation leaves nothing real to put in front of a judge, so it is
#      refused here, naming the quote — cheap gates expensive.
#
# This is an AFTER-CHECK (the guard is not preventive), so it only ever fires on a
# settled Post event: the bytes on disk ARE the answer.
#
# THE REFUSAL CONTRACT: exit 0 permits; non-zero refuses with `{"reason": "..."}`
# on stdout. Fails closed on the deterministic logic; the schema-read path stays
# out of the way for a malformed task (which the sibling rule already refuses).
set -uo pipefail

# refuse emits `{"reason": ...}` and exits non-zero, in the CURRENT shell — never
# behind a pipe (a piped refuse runs in a subshell and its exit would not stop the
# script, silently PERMITTING). Callers build the reason into a variable first: a
# STATIC tail via a QUOTED heredoc joined to a dynamic head in a double-quoted
# string, whose `$var` expansion is not recursive, so a `$(...)` in a body or quote
# is inert data.
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
# validated against the schema, never a hand-rolled frontmatter read that would be
# a second opinion about where frontmatter ends. --emit prints the validated
# frontmatter as JSON on success and NOTHING on failure — so a malformed task
# comes back with an empty status and this rule stays out of the way (correct:
# task-evidence-resolves is already refusing that write with a better message).
schema="$root/.sloprail/schemas/task.cue"
if [ ! -f "$schema" ]; then
  # Without the schema the status cannot be read. This is the guard's own
  # dependency missing, not a model flake — refuse, naming the fix.
  refuse "task-review: schema not found at $schema, so the task's status cannot be read. Install the plugin's task.cue under the project's .sloprail/schemas/."
fi

status="$(sr-file validate "$abs" --schema "$schema" --emit 2>/dev/null | jq -r '.status // empty' 2>/dev/null)"
[ "$status" = "in_review" ] || exit 0

# ------------------------------------------------------- the pre-flight gate ---
lib="$gdir/../task-evidence-resolves/cite-links.sh"
if [ ! -f "$lib" ]; then
  refuse "task-review: cite-links.sh not found at $lib, so no citation could be grounded for review"
fi
# shellcheck source=../task-evidence-resolves/cite-links.sh
. "$lib"

content="$(cat "$abs" 2>/dev/null)"

problems=""
n_links=0
while IFS="$(printf '\t')" read -r href quote; do
  [ -n "$href" ] || continue
  n_links=$((n_links + 1))
  cpath="$(cite_link_href_path "$href")"
  case "$cpath" in
    /*) : ;;
    *)  cpath="$root/$cpath" ;;
  esac
  if reason="$(cite_ground "$cpath" "$quote")"; then :; else
    problems="${problems}  ${reason}
"
  fi
done <<EOF
$(cite_links_extract "$content")
EOF

if [ "$n_links" -eq 0 ]; then
  IFS= read -r -d '' tail <<'EOF' || true

An in_review task claims the work is finished. There is nothing to review until it
grounds that claim in the user's own words about what was asked and what proves it
done, as markdown links:

    [the user's exact words](/abs/session.jsonl:120)

Each link's quote must resolve — via cite — to a real user message. The status
stays in_review; attach the evidence and write the task again.
EOF
  refuse "REVIEW CANNOT RUN: $path is in_review but carries no citation of the work.
$tail"
fi

if [ -n "$problems" ]; then
  IFS= read -r -d '' tail <<'EOF' || true
There is nothing to review until every citation resolves to the user's own words.
A citation is [<quote>](<jsonl-path>), the quote verbatim and the href the
transcript. Fix the quotes or the paths. The status stays in_review; correct the
evidence and write the task again.
EOF
  refuse "REVIEW CANNOT RUN: $path is in_review but its evidence does not ground.

$problems
$tail"
fi

exit 0
