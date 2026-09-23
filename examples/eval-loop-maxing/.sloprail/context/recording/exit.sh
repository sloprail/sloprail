#!/usr/bin/env bash
# exit: refuse the Stop until every eval run this session produced is documented.
# Each eval invocation's stdout named the run file it produced; this demands a
# markdown file somewhere referencing that run path.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# Every run path an eval command reported this session — an entry with a
# PreCommandInvoke `eval` invocation, run file named in its .toolUseResult.
run_paths="$(sr-session trajectory normalize \
  --path "$transcript_path" \
  --events PreCommandInvoke \
  | jq -r '.[]
      | select(any(.events[]?; .kind == "PreCommandInvoke"
          and any(.invocations[]?; .bin == "eval")))
      | (.toolUseResult // empty)
      | if type == "string" then . else tostring end' \
  | grep -E '^evals/runs/.*\.json$')"

if [ -z "$run_paths" ]; then
  # No eval run this session — nothing to document.
  exit 0
fi

missing=""
while IFS= read -r run; do
  [ -z "$run" ] && continue
  if ! grep -rlq "$run" --include="*.md" . 2>/dev/null; then
    missing="$missing $run"
  fi
done <<< "$run_paths"

if [ -n "$missing" ]; then
  cat <<EOF
{"decision":"block","reason":"These eval runs have no markdown documenting them (a file whose frontmatter references the run path):$missing. Every run this trajectory produced must be written up before the turn can end."}
EOF
  exit 1
fi

# Every run this session made is documented — done.
exit 0
