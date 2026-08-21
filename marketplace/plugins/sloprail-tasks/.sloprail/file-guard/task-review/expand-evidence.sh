#!/usr/bin/env bash
# prepare for task-review: hand the judge the task's stated CLAIM and the DELIVERY
# evidence, EXPANDED to the actual bytes, so review-task.md.j2 never has to parse a
# transcript or open a file itself. Receives the SAME CheckPayload the pre-flight
# did — but is a SEPARATE check, so it runs for EVERY status (a passing pre-flight
# does not end the chain). It gates itself on status first: a non-in_review task is
# SKIPPED here (see the skip branch below), so by the time evidence is assembled the
# task is in_review, every observation line is a tool_result, and every artifact
# resolves (the pre-flight already refused an in_review task whose evidence did not).
#
# THIS IS ABOUT DELIVERY, NOT THE ASK. The evidence assembled here is proof the work
# was DONE, weighed against the claim the task makes — it is NOT a re-citation of the
# user's request (that is task-body-is-human-authored's subject). Both kinds are
# FRONTMATTER citation strings, exactly the old-format review's $observations and
# $artifacts, and they differ in PATH BASE and expansion:
#
#   OBSERVATIONS — proof the work happened. Each is `<abs-jsonl>:<ranges>` into the
#   session transcript. For each cited LINE, `sr-session trajectory tool-result
#   --line` returns the tool_result's content — the test that came back green, the
#   command whose output is the evidence. Showing the tool_result content (and only
#   a line that IS one) is what lets the judge tell a real result from a narration.
#
#   ARTIFACTS — where the result is. Each is `<repo-relative-file>:<ranges>` into the
#   TREE; the cited lines are read straight off disk under the repo root, so the
#   judge reviews the produced result at the lines that changed.
#
# Output nests under `additionalContext` — the one key the engine reads. Emits
# .task_body (the whole task, the stated claim), .observations and .artifacts (the
# expanded evidence), and .evidence_ok (whether any evidence was assembled). All are
# agent- and tool-shaped text and are framed as DATA in the template.
#
# THE PREPARE CONTRACT: a non-zero exit fails the check closed. Exit 0 with
# `additionalContext` proceeds to the judge; exit 0 with `{"skip": true}` ABSTAINS —
# the judge is not invoked and this check reaches no verdict, so a non-in_review task
# never pays for a review it is not up for (see the skip branch below). An
# evidence-read failure on an in_review task is NOT a skip: it reports
# evidence_ok=false and lets the judge run, which the template treats as a fail
# rather than guessing at what it contained.
#
# Bounded like the old review: one task citing a 10,000-line range must not build an
# unbounded prompt. Truncation is ANNOUNCED inline so a judge looking at part of a
# slice knows it and can say the evidence was not legible (the template's legibility
# criterion reads that marker), rather than inventing a verdict about unseen bytes.
set -uo pipefail

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"
abs="$root/$path"

lib="$gdir/../task-evidence-resolves/cite-links.sh"
if [ ! -f "$lib" ]; then
  echo "task-review: cite-links.sh not found at $lib, so the evidence could not be assembled" >&2
  exit 1
fi
# shellcheck source=../task-evidence-resolves/cite-links.sh
. "$lib"

schema="$root/.sloprail/schemas/task.cue"

# Post kind — the file is present and the validated frontmatter is where the status
# and the two evidence lists are read.
task_body="$(cat "$abs" 2>/dev/null || true)"
doc="$(sr-file validate "$abs" --schema "$schema" --emit 2>/dev/null)"

# ONLY an in_review task is reviewed. The pre-flight (check 1) already exits 0 for a
# non-in_review task — but that only PASSES check 1; this judge is a SEPARATE check
# and would otherwise still run (a passing check does not end the chain, only a
# refusal does). So a to_do / in_progress / blocked / backlog task would pay for a
# full model call it is not up for. `{"skip": true}` makes this check ABSTAIN: the
# judge is not invoked and the check reaches no verdict, the same status gate the
# pre-flight applies, now applied to the judge too. Read from the SAME validated
# frontmatter the pre-flight uses (--emit is empty for a malformed task, so status is
# empty and this skips too — correct: task-evidence-resolves owns that refusal, and a
# malformed task is not one to review).
status="$(printf '%s' "$doc" | jq -r '.status // empty' 2>/dev/null)"
if [ "$status" != "in_review" ]; then
  printf '{"skip": true}\n'
  exit 0
fi

obs_lines="$(printf '%s' "$doc" | jq -r '(.observations // [])[]' 2>/dev/null)"
art_lines="$(printf '%s' "$doc" | jq -r '(.artifacts // [])[]' 2>/dev/null)"

# The same bounds the old review carried, for the same reason.
MAX_LINES_PER_CITATION=120
MAX_CHARS_PER_LINE=600
MAX_TOTAL_CHARS=60000
total_chars=0

# clip "<text>" -> the text truncated to the per-line cap, with the running total
# advanced. Sets clip_out and returns 1 once the total budget is spent, NOT via
# command substitution — $total_chars must accumulate across every line of every
# citation in this one process, and a subshell would reset it and let an unbounded
# prompt through.
clip_out=""
clip() {
  if [ "$total_chars" -ge "$MAX_TOTAL_CHARS" ]; then
    clip_out=""
    return 1
  fi
  clip_out="$(printf '%s' "$1" | cut -c "1-$MAX_CHARS_PER_LINE")"
  total_chars=$((total_chars + ${#clip_out} + 8))
  return 0
}

# ------------------------------------------------- expand the OBSERVATIONS ----
#
# Each observation's cited LINES are the tool_result content the session produced.
# tool-result --line returns that content for a line that IS a tool_result; the
# pre-flight already confirmed each line is one, so a miss here is this prepare's own
# re-read failing, reported inline rather than silently dropped.
observations=""
idx=0
while IFS= read -r obs; do
  [ -n "$obs" ] || continue
  opath="$(citation_path "$obs")"
  oranges="$(citation_ranges "$obs")"
  observations="${observations}### observations[$idx] ${obs}
"
  shown=0
  for n in $(citation_lines "$oranges"); do
    if [ "$shown" -ge "$MAX_LINES_PER_CITATION" ]; then
      observations="${observations}[TRUNCATED: this citation names more lines than were shown]
"
      break
    fi
    result="$(sr-session trajectory tool-result --path "$opath" --line "$n" 2>/dev/null)"
    if [ -z "$result" ]; then
      observations="${observations}${n}: [line grounded in pre-flight but its tool_result content could not be re-read here]
"
      shown=$((shown + 1))
      continue
    fi
    if clip "$result"; then
      observations="${observations}${n}: ${clip_out}
"
    else
      observations="${observations}[TRUNCATED: the evidence exceeded the size this reviewer can show]
"
      break
    fi
    shown=$((shown + 1))
  done
  observations="${observations}
"
  idx=$((idx + 1))
done <<EOF
$obs_lines
EOF

# ---------------------------------------------------- expand the ARTIFACTS ----
#
# Each artifact's cited tree lines are read straight off disk under the repo root —
# the produced result at the lines that changed.
artifacts=""
idx=0
while IFS= read -r art; do
  [ -n "$art" ] || continue
  apath="$(citation_path "$art")"
  aranges="$(citation_ranges "$art")"
  case "$apath" in
    /*) : ;;
    *)  apath="$root/$apath" ;;
  esac
  artifacts="${artifacts}### artifacts[$idx] ${art}
"
  shown=0
  for n in $(citation_lines "$aranges"); do
    if [ "$shown" -ge "$MAX_LINES_PER_CITATION" ]; then
      artifacts="${artifacts}[TRUNCATED: this citation names more lines than were shown]
"
      break
    fi
    body="$(sed -n "${n}p" "$apath" 2>/dev/null)"
    if clip "$body"; then
      artifacts="${artifacts}${n}: ${clip_out}
"
    else
      artifacts="${artifacts}[TRUNCATED: the evidence exceeded the size this reviewer can show]
"
      break
    fi
    shown=$((shown + 1))
  done
  artifacts="${artifacts}
"
  idx=$((idx + 1))
done <<EOF
$art_lines
EOF

evidence_ok=false
{ [ -n "$observations" ] || [ -n "$artifacts" ]; } && evidence_ok=true

jq -n \
  --arg body "$task_body" \
  --arg obs "$observations" \
  --arg art "$artifacts" \
  --argjson ok "$evidence_ok" \
  '{additionalContext: {task_body: $body, observations: $obs, artifacts: $art, evidence_ok: $ok}}'
