#!/usr/bin/env bash
# The residue pattern (unit 15's own words: same entity as deterministic
# refactoring, "I don't know how to semantically name it"). Collect every
# user message this turn, subtract those a task file references AND those
# the agent has explicitly marked as needing no task, refuse if anything is
# left — "either linked to tasks, or explicitly linked to nothing, but they
# have to be somewhere."
#
# The "explicitly linked to nothing" half is a #skip the agent declares in its
# prose, tracked by the sibling skip-declared context which logs each excused
# message into its own registry; this gate reads that registry via
# `state list --owner skip-declared`.
#
# 2026-08-20 (skip-channel redesign): an earlier draft had the AGENT run
# `sr-session state set skip:<ref>` directly and had this gate read skip: from
# its OWN scope. Neither worked — state set needs the SR_GUARDRAIL hook
# environment the agent's shell lacks ("no guardrail in scope"), and the gate's
# own scope is not where the agent could have written. The skip now goes through
# the skip-declared context (so the write happens inside a hook), and --owner
# (merged in b8608c3) is the read that reaches its registry. This gate stays on
# EVERY Stop and does NOT require the skip context — it must check the residue
# whether or not a skip was declared, and the Stop dispatch order (context
# enters before Stop gates) makes a #skip declared this cycle visible here
# anyway.
set -uo pipefail

ws="${SR_WORKSPACE:-.}"

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
# shape. tasks/ is resolved under $SR_WORKSPACE (the tree being guarded): a
# check runs with cwd = this guardrail's own folder, not the repo root, so a
# bare `tasks/` would look in the wrong place (2026-08-20 fix, same
# ${SR_WORKSPACE:-.} anchor the working eval-loop-maxing example uses).
referenced="$(grep -rohE '\(/[^)]+:[0-9]+-[0-9]+\)' "$ws/tasks" 2>/dev/null | tr -d '()' | sort -u)"

# Every message the agent has explicitly marked as needing no task — logged by
# the sibling skip-declared context (which enters on the agent's #skip and keys
# each excused message as skip:<transcript>:<line>-<line>). Read across the
# per-guardrail boundary with --owner skip-declared; `state list` emits JSON
# lines, so it is slurped with `jq -s` before being treated as a stream.
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
