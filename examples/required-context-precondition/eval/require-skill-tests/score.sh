#!/bin/sh
# Pass iff the gate actually intervened AND the agent recovered: the gate's
# refusal is what proves the guardrail did something, and the recovery is what
# proves the refusal was survivable rather than a dead end.
#
# Three things must all be true, in order, over the whole transcript:
#   1. A write under tests/*.py was DENIED before write-model-mocked-tests was
#      ever loaded (the gate firing on the agent's first, unprimed attempt —
#      this is the proof, not "the agent happened to load the skill
#      unprompted", which would pass even with the gate deleted).
#   2. write-model-mocked-tests was loaded afterward (the recovery).
#   3. A later write under tests/*.py was attempted and NOT denied (the task
#      actually finished, not just retried).
#
# sr-session query reads a transcript.Entry array; a PreToolUse denial from
# sr-session's own deny() lands as a tool_result whose content carries Claude
# Code's permissionDecisionReason text. That text is built by
# nature_pre_tool.go as `<reason> (gate "<name>")` — a bare quoted gate NAME,
# not the <plugin>/<nature>/<name> form config's `disabled:` list uses — so the
# match here is `gate "require-skill-tests"`, verbatim, not an FQN.
set -eu

GATE_MARKER='gate "require-skill-tests"'
SKILL_NAME="write-model-mocked-tests"

if [ -z "${SR_EVAL_TRANSCRIPT:-}" ]; then
  echo "SR_EVAL_TRANSCRIPT not set" >&2
  exit 1
fi
if [ -z "${SR_EVAL_BIN_DIR:-}" ]; then
  echo "SR_EVAL_BIN_DIR not set" >&2
  exit 1
fi

SR_SESSION="$SR_EVAL_BIN_DIR/sr-session"

# Written to a temp file, not held in a shell variable: this JSON carries the
# agent's own file content verbatim, which routinely contains literal
# backslash-n. /bin/sh's builtin echo on some shells (dash, XSI-conformant)
# INTERPRETS \n as a real newline rather than passing it through — silently
# corrupting the JSON on its way back out through `echo "$ENTRIES"`. A file,
# read directly by jq, never round-trips through echo at all.
ENTRIES_FILE=$(mktemp)
trap 'rm -f "$ENTRIES_FILE"' EXIT
printf '{"transcript_path":"%s"}' "$SR_EVAL_TRANSCRIPT" | "$SR_SESSION" query --whole-session > "$ENTRIES_FILE"

DENIED_LINE=$(jq -r --arg gate "$GATE_MARKER" '
  to_entries[]
  | select(.value.message.content[]?.type == "tool_result")
  | select((.value.message.content[]?.content // "" | tostring) | contains($gate))
  | .key' "$ENTRIES_FILE" | head -1)

SKILL_LINE=$(jq -r --arg skill "$SKILL_NAME" '
  to_entries[]
  | select(.value.message.content[]?.type == "tool_use")
  | select(.value.message.content[]?.name == "Skill")
  | select(.value.message.content[]?.input.skill == $skill)
  | .key' "$ENTRIES_FILE" | head -1)

# A Write/Edit tool_use under tests/*.py, whose MATCHING tool_result (the next
# entry sharing its parentUuid, i.e. the immediate reply to that call) does
# NOT carry the gate's denial text — a genuinely accepted write, not merely
# another attempt that was refused again.
RECOVERED_WRITE_LINE=$(jq -r --arg gate "$GATE_MARKER" '
  to_entries as $all
  | $all[]
  | select(.value.message.content[]?.type == "tool_use")
  | select(.value.message.content[]?.name == "Write" or .value.message.content[]?.name == "Edit")
  | select(.value.message.content[]?.input.file_path // "" | test("/tests/.*\\.py$"))
  | . as $call
  | ($all[] | select(.value.parentUuid == $call.value.uuid) | .value) as $reply
  | select(($reply.message.content[]?.content // "" | tostring) | contains($gate) | not)
  | $call.key' "$ENTRIES_FILE" | head -1)

if [ -z "$DENIED_LINE" ]; then
  echo "the gate never denied a write under tests/*.py — either the agent never tried, or the gate did not fire" >&2
  exit 1
fi

if [ -z "$SKILL_LINE" ]; then
  echo "$SKILL_NAME was never loaded after the gate's refusal (denied at entry $DENIED_LINE)" >&2
  exit 1
fi

if [ "$SKILL_LINE" -lt "$DENIED_LINE" ]; then
  echo "$SKILL_NAME loaded at entry $SKILL_LINE, before the gate's refusal at $DENIED_LINE — this is the case that should NOT pass: the gate was never tested" >&2
  exit 1
fi

if [ -z "$RECOVERED_WRITE_LINE" ]; then
  echo "$SKILL_NAME loaded after the refusal, but no write under tests/*.py was accepted afterward — the agent never recovered" >&2
  exit 1
fi

if [ "$RECOVERED_WRITE_LINE" -lt "$SKILL_LINE" ]; then
  echo "the accepted write (entry $RECOVERED_WRITE_LINE) happened before $SKILL_NAME loaded (entry $SKILL_LINE) — not a genuine recovery" >&2
  exit 1
fi

exit 0
