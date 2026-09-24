#!/bin/sh
# Pass iff the gate actually intervened AND the agent recovered: the gate's
# refusal is what proves the guardrail did something, and the recovery is what
# proves the refusal was survivable rather than a dead end.
#
# Three checks, each independent, over the whole transcript:
#   RT-001 denied_first_attempt      — a write under tests/*.py was DENIED
#                                       before write-model-mocked-tests was
#                                       ever loaded (the gate firing on the
#                                       agent's first, unprimed attempt — this
#                                       is the proof, not "the agent happened
#                                       to load the skill unprompted", which
#                                       would pass even with the gate deleted)
#   RT-002 skill_loaded_after_denial — the skill was loaded AFTER that denial
#   RT-003 recovered_write           — a later write under tests/*.py was
#                                       accepted, not denied, after the load
#
# sr-session query reads a transcript.Entry array; a PreToolUse denial from
# sr-session's own deny() lands as a tool_result whose content carries Claude
# Code's permissionDecisionReason text. That text is built by
# nature_pre_tool.go as `<reason> (gate "<name>")` — a bare quoted gate NAME,
# not the <plugin>/<nature>/<name> form config's `disabled:` list uses — so the
# match here is `gate "require-skill-tests"`, verbatim, not an FQN.
#
# Every check's own pass/fail and reasoning is written to
# $SR_EVAL_VERDICT_OUT, pass or fail, whole-run — {subject, status, rows},
# a10n-eval's own scorer verdict.json shape, unchanged field names. Exit
# code stays the authority sr-eval itself reads.
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

# --- RT-001: denied_first_attempt ---
if [ -n "$DENIED_LINE" ]; then
  rt001_status="pass"
  rt001_reason="the gate denied a write under tests/*.py at transcript entry $DENIED_LINE"
else
  rt001_status="fail"
  rt001_reason="the gate never denied a write under tests/*.py — either the agent never tried, or the gate did not fire"
fi

# --- RT-002: skill_loaded_after_denial ---
if [ -z "$SKILL_LINE" ]; then
  rt002_status="fail"
  rt002_reason="$SKILL_NAME was never loaded"
elif [ -z "$DENIED_LINE" ]; then
  rt002_status="fail"
  rt002_reason="$SKILL_NAME loaded at entry $SKILL_LINE, but RT-001 never denied anything to load it after"
elif [ "$SKILL_LINE" -lt "$DENIED_LINE" ]; then
  rt002_status="fail"
  rt002_reason="$SKILL_NAME loaded at entry $SKILL_LINE, BEFORE the gate's refusal at $DENIED_LINE — the gate was never tested"
else
  rt002_status="pass"
  rt002_reason="$SKILL_NAME loaded at entry $SKILL_LINE, after the refusal at $DENIED_LINE"
fi

# --- RT-003: recovered_write ---
if [ -z "$RECOVERED_WRITE_LINE" ]; then
  rt003_status="fail"
  rt003_reason="no write under tests/*.py was ever accepted"
elif [ "$rt002_status" != "pass" ]; then
  rt003_status="fail"
  rt003_reason="a write under tests/*.py was accepted at entry $RECOVERED_WRITE_LINE, but RT-002 did not pass — not a genuine recovery"
elif [ "$RECOVERED_WRITE_LINE" -lt "$SKILL_LINE" ]; then
  rt003_status="fail"
  rt003_reason="the accepted write at entry $RECOVERED_WRITE_LINE happened BEFORE $SKILL_NAME loaded at entry $SKILL_LINE"
else
  rt003_status="pass"
  rt003_reason="a write under tests/*.py was accepted at entry $RECOVERED_WRITE_LINE, after $SKILL_NAME loaded at entry $SKILL_LINE"
fi

overall="pass"
[ "$rt001_status" = "pass" ] && [ "$rt002_status" = "pass" ] && [ "$rt003_status" = "pass" ] || overall="fail"

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "require-skill-tests" \
    --arg status "$overall" \
    --arg rt001s "$rt001_status" --arg rt001r "$rt001_reason" \
    --arg rt002s "$rt002_status" --arg rt002r "$rt002_reason" \
    --arg rt003s "$rt003_status" --arg rt003r "$rt003_reason" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "RT-001-denied_first_attempt", status: $rt001s, reasoning: $rt001r},
       {check_id: "RT-002-skill_loaded_after_denial", status: $rt002s, reasoning: $rt002r},
       {check_id: "RT-003-recovered_write", status: $rt003s, reasoning: $rt003r}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

if [ "$overall" != "pass" ]; then
  echo "$rt001_reason; $rt002_reason; $rt003_reason" >&2
  exit 1
fi

exit 0
