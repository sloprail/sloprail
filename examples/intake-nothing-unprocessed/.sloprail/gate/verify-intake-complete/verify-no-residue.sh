#!/usr/bin/env bash
# The residue pattern (unit 15's own words: same entity as deterministic
# refactoring, "I don't know how to semantically name it"). Collect every
# user message this turn, subtract those a task file references AND those
# the agent has explicitly marked as needing no task, refuse if anything is
# left — "either linked to tasks, or explicitly linked to nothing, but they
# have to be somewhere."
#
# The "explicitly linked to nothing" half has no tag or context of its own
# (his correction, 2026-08-19, after an earlier draft invented one: "let the
# agent just use that sr session state then to set these messages") — the
# agent itself calls `sr-session state set skip:<ref> <reason>` when a
# message needs no task, the same registry pattern used everywhere else in
# this corpus, just written directly rather than through a context's enter.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# Every user message this turn, addressed as /abs/path:start-end — same
# shape unit 05's grounding citations settled on (2026-08-19: absolute path,
# not a bare id, because a session can span multiple jsonl files and a bare
# id says nothing about WHICH transcript it lives in).
#
# A user message is an entry with .type == "user"; .line is its real 1-based
# jsonl position, present on every normalized entry, which is what turns a
# message into a locatable /abs/path:line-line reference.
all_message_refs="$(sr-session trajectory normalize \
  --path "$transcript_path" \
  | jq -r --arg t "$transcript_path" '.[] | select(.type == "user") | "\($t):\(.line)-\(.line)"')"

if [ -z "$all_message_refs" ]; then
  exit 0
fi

# Every message a task file's ASK.md references — same /abs/path:start-end
# shape.
referenced="$(grep -rohE '\(/[^)]+:[0-9]+-[0-9]+\)' tasks/ 2>/dev/null | tr -d '()' | sort -u)"

# Every message the agent has explicitly marked as needing no task — logged
# directly via `sr-session state set skip:<ref> <reason>`, this gate's own
# name (verify-intake-complete) is its SR_GUARDRAIL so `state list` reads
# back everything logged under it this session.
skipped="$(sr-session state list 2>/dev/null \
  | jq -r '[.[] | select(.key | startswith("skip:"))] | .[].key | ltrimstr("skip:")')"

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
