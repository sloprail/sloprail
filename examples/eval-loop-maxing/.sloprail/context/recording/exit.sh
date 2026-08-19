#!/usr/bin/env bash
# exit: is every eval run this session produced actually documented? A
# completeness check (the primitive named in decision
# 20260818_no-slop-primitives), realised as a plain script — no special
# entity, per his "начать с того, что это просто скрипты."
#
# For every eval command invocation found in the trajectory, the tool's own
# stdout named the run file it produced — proof the run happened, not a
# claim. This check demands a markdown file whose frontmatter references
# that run path exists somewhere in the tree. Refuses the Stop and re-enters
# next time an eval command runs, until every run this session made is
# documented.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# Every run path an eval command reported, this session.
run_paths="$(sr-session query \
  --transcript "$transcript_path" \
  --select tool_use \
  --where 'any(invocations, .bin == "eval")' \
  | jq -r '.[].output // empty' \
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
