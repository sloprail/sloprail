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
if [ -z "$transcript_path" ] || [ "$transcript_path" = "null" ]; then
  echo "verify-no-residue: could not read .transcriptPath from the hook payload; refusing because a residue check that cannot see the transcript must not be read as approval" >&2
  exit 1
fi

# Every GENUINE user message this turn as /abs/path:line-line — absolute path,
# not a bare id, since a session can span multiple jsonl files. A genuine user
# message is an entry with .type == "user", isMeta NOT true, and not made up ONLY of tool_result
# content blocks (an entry that also carries a text block is still the person's); .line is its 1-based jsonl position.
#
# A tool_result block rides in a `type: "user"` entry too: a tool's output, and
# also an AskUserQuestion answer envelope ("The user answered: ..."), re-enter
# the transcript that way. Neither is something the person typed as a request,
# so neither can be residue the agent owes a task or a #skip for; counting them
# made the gate demand tasks for tool output.
#
# isMeta excludes a real, previously-hit failure mode: Claude Code records a
# Stop hook's OWN refusal text ("Stop hook feedback: These user messages are
# not mapped to any task...") as a plain `type: "user"` entry, shape-identical
# to something the person typed (internal/transcript/entry.go's own IsMeta doc
# comment names this exact case). Without excluding it, every refusal this gate
# emits becomes a NEW unresolved "user message" on the very next cycle: the
# agent skips the real residue, the gate's own refusal about it becomes fresh
# residue, the agent skips THAT, ad infinitum — measured directly in a real
# Haiku run (examples/intake-nothing-unprocessed/eval/multi-ask-turn): #skip 47
# begat a residue at line 51 (the refusal that had just fired), #skip 51 begat
# 57, and so on for 7 cycles with no way out, regardless of how the agent
# responded — a structural trap this check created, not an agent failure to
# fix by teaching a better response.
normalized="$(sr-session trajectory normalize --path "$transcript_path")"
normalize_status=$?
if [ "$normalize_status" -ne 0 ]; then
  echo "verify-no-residue: sr-session trajectory normalize failed (exit $normalize_status) on $transcript_path; refusing because a residue check that could not read the transcript must not be read as approval" >&2
  exit 1
fi

all_message_refs="$(printf '%s' "$normalized" \
  | jq -r --arg t "$transcript_path" '.[] | select(.type == "user" and (.isMeta // false) == false and (((.message.content? // "") | if type == "array" then (length > 0 and all(.[]; type == "object" and .type == "tool_result")) else false end) | not)) | "\($t):\(.line)-\(.line)"')"
jq_status=$?
if [ "$jq_status" -ne 0 ]; then
  echo "verify-no-residue: could not extract user messages from the normalized transcript (jq exit $jq_status); refusing because a residue check that could not parse the transcript must not be read as approval" >&2
  exit 1
fi

if [ -z "$all_message_refs" ]; then
  # A genuinely empty transcript (no user messages at all) is the one case
  # this is allowed to read as "no residue" — every error path above already
  # refused before reaching here.
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
  echo "These user messages are not mapped to any task, and none was marked skip. A message is mapped only by a task FILE under tasks/ (tasks/<name>.md or tasks/<name>/<file>.md) whose text contains the message's reference in parentheses, as (<transcript>:L-L), exactly as listed below. Native TaskCreate/TodoWrite entries do NOT count as mapping a message. To skip one instead, write a message containing '#skip <line-number>' naming its 1-based line in the transcript (not a direct sr-session state set call — that needs the hook environment your shell does not have; the skip-declared context reads your #skip tag for you):$residue" >&2
  exit 1
fi

exit 0
