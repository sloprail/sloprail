#!/usr/bin/env bash
# prepare for task-review: hand the judge the task's stated CLAIM and the DELIVERY
# evidence, EXPANDED to the actual bytes, so review-task.md.j2 never has to read a
# transcript or open a file itself. Receives the SAME CheckPayload the pre-flight
# did — but is a SEPARATE check, so it runs for EVERY status (a passing pre-flight
# does not end the chain). It gates itself on status first: a non-in_review task is
# SKIPPED here (see the skip branch below), so by the time evidence is assembled the
# task is in_review, at least one tool output is cited, and every artifact resolves
# (the pre-flight already refused an in_review task whose evidence did not).
#
# THIS IS ABOUT DELIVERY, NOT THE ASK. The evidence assembled here is proof the work
# was DONE, weighed against the claim the task makes — it is NOT the user's request
# (that is task-body-is-human-authored's subject). Two kinds, from two places:
#
#   CITED TOOL RESULTS — proof the work happened. The write that made the claim
#   carried `--cite:tool_result '<exact output>'`; the session resolved each quote
#   to one tool_result of its record and the event carries it as a citation
#   `{quote, sourceTypes, path, line}` (at Stop: the citations recorded for this
#   path at pre-tool). For each one whose sourceTypes include tool_result, the
#   judge is shown the quote and the FULL tool_result at that line, fetched with
#   `sr-session trajectory tool-result --path <path> --line <line>` — the test run
#   the quote came from, not just the line the agent chose to quote.
#
#   ARTIFACTS — where the result is. Each is `<repo-relative-file>:<ranges>` in the
#   frontmatter; the cited lines are read straight off disk under the repo root, so
#   the judge reviews the produced result at the lines that changed.
#
# Output nests under `additionalContext` — the one key the engine reads. Emits
# .task_body (the whole task, the stated claim), .cited_results and .artifacts (the
# expanded evidence), .judgment_gates (every gates/*.md file's text — the task's
# own start conditions, re-weighed HERE at the in_review claim rather than a
# second judge call; gates/*.sh gates are NOT here, review-preflight.sh already
# ran them deterministically before this check is reached), and .evidence_ok
# (whether any evidence was assembled). All are agent- and tool-shaped text and
# are framed as DATA in the template.
#
# THE PREPARE CONTRACT: a non-zero exit fails the check closed. Exit 0 with
# `additionalContext` proceeds to the judge; exit 0 with `{"skip": true}` ABSTAINS —
# the judge is not invoked and this check reaches no verdict, so a non-in_review task
# never pays for a review it is not up for. An evidence-read failure on an in_review
# task is NOT a skip: it is reported inline (or as evidence_ok=false) and the judge
# runs, which the template treats as a fail rather than guessing at what it held.
#
# Bounded like the old review: one tool result or one artifact range of 10,000
# lines must not build an unbounded prompt. Truncation is ANNOUNCED inline so a
# judge looking at part of a slice knows it and can say the evidence was not
# legible (the template's legibility criterion reads that marker), rather than
# inventing a verdict about unseen bytes.
set -uo pipefail

command -v jq >/dev/null 2>&1 || {
  echo "task-review: jq is not on PATH, so the evidence could not be assembled" >&2
  exit 1
}

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

# The schema is the PLUGIN's, read from the plugin's own tree — see
# task-evidence-resolves/check-task.sh for why it is never a consumer-side copy.
schema="$gdir/../../schemas/task.cue"

# Post kind — the file is present and the validated frontmatter is where the status
# and the artifacts are read.
task_body="$(cat "$abs" 2>/dev/null || true)"
doc="$(sr-file validate "$abs" --schema "$schema" --emit 2>/dev/null)"

# ONLY an in_review task is reviewed. The pre-flight (check 1) already exits 0 for a
# non-in_review task — but that only PASSES check 1; this judge is a SEPARATE check
# and would otherwise still run (a passing check does not end the chain, only a
# refusal does). `{"skip": true}` makes this check ABSTAIN: no model call, no
# verdict. Read from the SAME validated frontmatter the pre-flight uses (--emit is
# empty for a malformed task, so status is empty and this skips too — correct:
# task-evidence-resolves owns that refusal, and a malformed task is not one to
# review).
status="$(printf '%s' "$doc" | jq -r '.status // empty' 2>/dev/null)"
if [ "$status" != "in_review" ]; then
  printf '{"skip": true}\n'
  exit 0
fi

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

# --------------------------------------------- expand the CITED TOOL RESULTS ----
#
# Each tool_result citation's quote, and the whole tool_result it resolved to. The
# session already confirmed the quote lands on exactly one tool_result, so a miss
# here is this prepare's own re-read failing — reported inline rather than
# silently dropped, so the judge can say the evidence was not legible.
cited_results=""
idx=0
while IFS= read -r c; do
  [ -n "$c" ] || continue
  quote="$(printf '%s' "$c" | jq -r '.quote')"
  cpath="$(printf '%s' "$c" | jq -r '.path')"
  cline="$(printf '%s' "$c" | jq -r '.line')"
  # The call that printed the output: `echo 'tests passed'` proves nothing.
  ccall="$(printf '%s' "$c" | jq -r '.call // ""')"
  cited_results="${cited_results}### cited_results[$idx] ${cpath}:${cline}
produced by: ${ccall:-(unknown)}
quoted: ${quote}
"
  result="$(sr-session trajectory tool-result --path "$cpath" --line "$cline" 2>/dev/null)"
  if [ -z "$result" ]; then
    cited_results="${cited_results}[the citation resolved when the write was made, but its tool_result could not be re-read here]

"
    idx=$((idx + 1))
    continue
  fi
  cited_results="${cited_results}full tool result:
"
  shown=0
  while IFS= read -r rline; do
    if [ "$shown" -ge "$MAX_LINES_PER_CITATION" ]; then
      cited_results="${cited_results}[TRUNCATED: this tool result has more lines than were shown]
"
      break
    fi
    if clip "$rline"; then
      cited_results="${cited_results}${clip_out}
"
    else
      cited_results="${cited_results}[TRUNCATED: the evidence exceeded the size this reviewer can show]
"
      break
    fi
    shown=$((shown + 1))
  done <<EOF
$result
EOF
  cited_results="${cited_results}
"
  idx=$((idx + 1))
done <<EOF
$(printf '%s' "$event" | jq -c '(.event.citations // [])[] | select(((.sourceTypes // []) | index("tool_result")) != null)' 2>/dev/null)
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

# ------------------------------------------- collect the JUDGMENT gates ----
#
# gates/*.md files, verbatim, so the SAME judge call that weighs the delivery
# evidence also decides whether each judgment gate still holds — one model
# call, not two. .sh gates are NOT here: review-preflight.sh already ran them
# deterministically above and refused the write if any failed, so a judgment
# gate is the only kind left for a judge to weigh.
task_dir="$(dirname "$path")"
gates_dir="$root/$task_dir/gates"
judgment_gates=""
if [ -d "$gates_dir" ]; then
  while IFS= read -r -d '' g; do
    gname="$(basename "$g")"
    gbody="$(cat "$g" 2>/dev/null || true)"
    judgment_gates="${judgment_gates}### gates/${gname}
${gbody}

"
  done < <(find "$gates_dir" -maxdepth 1 -name '*.md' -type f -print0 2>/dev/null | sort -z)
fi

evidence_ok=false
{ [ -n "$cited_results" ] || [ -n "$artifacts" ]; } && evidence_ok=true

jq -n \
  --arg body "$task_body" \
  --arg results "$cited_results" \
  --arg art "$artifacts" \
  --arg gates "$judgment_gates" \
  --argjson ok "$evidence_ok" \
  '{additionalContext: {task_body: $body, cited_results: $results, artifacts: $art, judgment_gates: $gates, evidence_ok: $ok}}'
