#!/usr/bin/env bash
# The residue pattern: collect every user message this turn, subtract those a
# task file references AND those the sibling skip-declared context excused (read
# via `state list --owner skip-declared`), refuse if anything is left.
#
# Runs on EVERY Stop and does NOT require the skip context — it must check the
# residue regardless, and the Stop order (context enters before Stop gates)
# makes a #skip declared this cycle visible here anyway.
set -uo pipefail

ws="${SR_WORKSPACE:-.}"

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# Every user message this turn as /abs/path:line-line — absolute path, not a
# bare id, since a session can span multiple jsonl files. A user message is an
# entry with .type == "user"; .line is its 1-based jsonl position.
all_message_refs="$(sr-session trajectory normalize \
  --path "$transcript_path" \
  | jq -r --arg t "$transcript_path" '.[] | select(.type == "user") | "\($t):\(.line)-\(.line)"')"

if [ -z "$all_message_refs" ]; then
  exit 0
fi

# Every message a task file references — same shape. tasks/ is anchored on
# $SR_WORKSPACE because a check runs with cwd = its own guardrail folder, not
# the repo root.
referenced="$(grep -rohE '\(/[^)]+:[0-9]+-[0-9]+\)' "$ws/tasks" 2>/dev/null | tr -d '()' | sort -u)"

# Every message the skip-declared context excused, read across the per-guardrail
# boundary with --owner. `state list` emits JSON lines, so slurp with `jq -s`.
skipped="$(sr-session state list --owner skip-declared 2>/dev/null \
  | jq -s -r '[.[] | select(.key | startswith("skip:"))] | .[].key | ltrimstr("skip:")')"

accounted_for="$(printf '%s\n%s' "$referenced" "$skipped" | sed '/^$/d' | sort -u)"

residue=""
while IFS= read -r ref; do
  [ -z "$ref" ] && continue
  if ! grep -qx "$ref" <<< "$accounted_for"; then
    residue="$residue $ref"
  fi
done <<< "$all_message_refs"

if [ -n "$residue" ]; then
  echo "These user messages are not mapped to any task, and none was marked skip via 'sr-session state set skip:<ref> <reason>':$residue" >&2
  exit 1
fi

exit 0
