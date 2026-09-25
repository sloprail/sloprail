#!/usr/bin/env bash
# Refuses the Stop until every eval run this session produced is documented.
# `require` guarantees recording ran first, so this only needs the transcript —
# each eval invocation's own stdout named the run file it produced.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# The whole trajectory, raw entries (not --events PreCommandInvoke alone): a
# Bash tool_use's own stdout is NOT on that tool_use's event — it lands on the
# SEPARATE entry carrying the matching tool_result block, correlated by
# tool_use_id (the same join action-proof's find-action-and-proof.sh uses for a
# screenshot). And a REAL executed command's result — as opposed to a
# synthesized artifact a tool like `screenshot` returns — carries its stdout on
# that tool_result block's OWN `.content` field, not on a top-level
# `.toolUseResult` (empty for a genuinely-run shell command; `.toolUseResult` is
# populated only for tools that hand back a structured artifact). Reading
# `.toolUseResult` here (the original version's bug, and the same mistake
# research-rigor's depth-check/verify-depth.sh already documents fixing) finds
# nothing for every real eval run, so this gate always read "no runs" and
# admitted the Stop regardless of whether one had actually run. A second,
# independent bug in the original: `.invocations` bare, not `.fields.invocations`
# — every event's own fields sit under `.fields` in a normalized entry (see any
# entry's `.events[].fields`), so that filter never selected a PreCommandInvoke
# event either, masked by the toolUseResult bug always short-circuiting first.
normalized="$(sr-session trajectory normalize --path "$transcript_path" 2>&1)"
normalize_status=$?
if [ "$normalize_status" -ne 0 ]; then
  echo "recording-verify could not read this session's eval-run history (sr-session trajectory normalize exited $normalize_status: $normalized) — refusing rather than treating that as no runs to document." >&2
  exit 1
fi

filtered="$(printf '%s' "$normalized" | jq -r '
  [ .[] | select(any(.events[]?; .kind == "PreCommandInvoke"
        and any(.fields.invocations[]?; .bin == "eval")))
      | (.message | objects | .content // [] | if type == "array" then .[] else empty end)
      | select(.type == "tool_use") | .id ] as $eval_ids
  | .[] | (.message | objects | .content // [] | if type == "array" then .[] else empty end)
  | select(.type == "tool_result" and (.tool_use_id as $id | $eval_ids | index($id)))
  | .content
  | if type == "string" then . else tostring end' 2>&1)"
jq_status=$?
if [ "$jq_status" -ne 0 ]; then
  echo "recording-verify could not parse this session's eval-run history (jq exited $jq_status: $filtered) — refusing rather than treating that as no runs to document." >&2
  exit 1
fi

run_paths="$(printf '%s' "$filtered" | grep -E '^evals/runs/.*\.json$' || true)"

if [ -z "$run_paths" ]; then
  # No eval run this session — nothing to document, permit the Stop.
  exit 0
fi

missing=""
while IFS= read -r run; do
  [ -z "$run" ] && continue
  if ! grep -rlq "$run" --include="*.md" "${SR_WORKSPACE:-.}" 2>/dev/null; then
    missing="$missing $run"
  fi
done <<< "$run_paths"

if [ -n "$missing" ]; then
  echo "{\"reason\":\"These eval runs have no markdown documenting them (a file referencing the run path):$missing. Every run this trajectory produced must be written up before the turn can end.\"}"
  exit 1
fi

# Every run this session made is documented — permit the Stop.
exit 0
