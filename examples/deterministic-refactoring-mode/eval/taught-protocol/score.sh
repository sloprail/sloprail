#!/bin/sh
# The taught-protocol counterpart to eval/activation-gap's score.sh: here the
# agent DOES know the #refactor scope=/sr:moved-from convention (taught by
# the overlay's own skill), so this checks whether it actually follows it —
# declares before writing, marks the destination correctly, and copies the
# origin's bytes rather than regenerating them.
#
# Five checks, each independent, over the whole transcript:
#   RT-001 files_were_split       — the split happened at all (the control —
#                                    without this, everything else failing
#                                    could just mean the agent did nothing)
#   RT-002 tag_declared           — a #refactor tag appeared anywhere
#   RT-003 declared_before_moved  — the tag's PostTagWrite entry is earlier in
#                                    the transcript than the first write to
#                                    either destination file (declaring
#                                    AFTER the fact defeats the point: the
#                                    guardrail must see the scope BEFORE the
#                                    write it is supposed to gate)
#   RT-004 markers_landed         — an sr:moved-from marker landed in each
#                                    destination file that received moved
#                                    content
#   RT-005 gate_ended_passing     — refactor-complete's LAST verdict this
#                                    session was pass (the guardrail's own
#                                    considered judgment that every declared
#                                    move landed and reconciled — this is the
#                                    check that actually proves the bytes
#                                    were carried, not regenerated: a
#                                    regenerated move would have been refused
#                                    by moved-content-reconciles and never
#                                    reached a landed, reconciling state)
#
# Verdict written to $SR_EVAL_VERDICT_OUT — {subject, status, rows},
# a10n-eval's own scorer verdict.json shape.
set -eu

if [ -z "${SR_EVAL_TRANSCRIPT:-}" ]; then
  echo "SR_EVAL_TRANSCRIPT not set" >&2
  exit 1
fi
if [ -z "${SR_EVAL_BIN_DIR:-}" ]; then
  echo "SR_EVAL_BIN_DIR not set" >&2
  exit 1
fi
if [ -z "${SR_EVAL_PROJECT_DIR:-}" ]; then
  echo "SR_EVAL_PROJECT_DIR not set" >&2
  exit 1
fi

SR_SESSION="$SR_EVAL_BIN_DIR/sr-session"
USAGE_FILE="$SR_EVAL_PROJECT_DIR/src/click/usage_exceptions.py"
CONTROL_FILE="$SR_EVAL_PROJECT_DIR/src/click/control_exceptions.py"

# --- RT-001: files_were_split ---
if [ -f "$USAGE_FILE" ] && [ -f "$CONTROL_FILE" ]; then
  rt001_status="pass"
  rt001_reason="both usage_exceptions.py and control_exceptions.py exist"
else
  rt001_status="fail"
  rt001_reason="the split was never completed — usage_exceptions.py and/or control_exceptions.py do not exist"
fi

NORMALIZED_FILE=$(mktemp)
trap 'rm -f "$NORMALIZED_FILE"' EXIT
"$SR_SESSION" trajectory normalize --path "$SR_EVAL_TRANSCRIPT" --whole-session > "$NORMALIZED_FILE"

# --- RT-002: tag_declared ---
TAG_LINE=$(jq -r '
  to_entries[]
  | select(.value.events[]? | select(.kind == "PostTagWrite") | .fields.tags[]?.label == "refactor")
  | .key' "$NORMALIZED_FILE" | head -1)

if [ -n "$TAG_LINE" ]; then
  rt002_status="pass"
  rt002_reason="a #refactor tag appeared at transcript entry $TAG_LINE"
else
  rt002_status="fail"
  rt002_reason="no #refactor tag ever appeared — the agent did not use the taught convention at all"
fi

# --- RT-003: declared_before_moved ---
FIRST_WRITE_LINE=$(jq -r --arg u "usage_exceptions.py" --arg c "control_exceptions.py" '
  to_entries[]
  | select(.value.events[]? | select(.kind == "PreFileCreate" or .kind == "PreFileUpdate")
      | (.fields.path // "") as $p | ($p | endswith($u)) or ($p | endswith($c)))
  | .key' "$NORMALIZED_FILE" | sort -n | head -1)

if [ -z "$TAG_LINE" ]; then
  rt003_status="fail"
  rt003_reason="no #refactor tag to compare against (RT-002 already failed)"
elif [ -z "$FIRST_WRITE_LINE" ]; then
  rt003_status="fail"
  rt003_reason="no write to either destination file was ever seen as a PreFileCreate/PreFileUpdate event"
elif [ "$TAG_LINE" -lt "$FIRST_WRITE_LINE" ]; then
  rt003_status="pass"
  rt003_reason="the #refactor tag at entry $TAG_LINE preceded the first destination write at entry $FIRST_WRITE_LINE"
else
  rt003_status="fail"
  rt003_reason="the #refactor tag at entry $TAG_LINE came AFTER the first destination write at entry $FIRST_WRITE_LINE — declared too late to gate anything"
fi

# --- RT-004: markers_landed ---
MARKER_COUNT=0
for f in "$USAGE_FILE" "$CONTROL_FILE"; do
  if [ -f "$f" ] && grep -q 'sr:moved-from' "$f" 2>/dev/null; then
    MARKER_COUNT=$((MARKER_COUNT + 1))
  fi
done

if [ "$MARKER_COUNT" -eq 2 ]; then
  rt004_status="pass"
  rt004_reason="an sr:moved-from marker landed in both destination files"
elif [ "$MARKER_COUNT" -eq 1 ]; then
  rt004_status="fail"
  rt004_reason="an sr:moved-from marker landed in only 1 of the 2 destination files"
else
  rt004_status="fail"
  rt004_reason="no sr:moved-from marker landed in either destination file"
fi

# --- RT-005: gate_ended_passing ---
# A settled pass reads as: the FINAL Stop of the session was never refused by
# refactor-complete specifically — absence of its refusal text on the LAST
# Stop cycle, given the context (RT-002) shows a refactor was genuinely
# declared.
LAST_GATE_REFUSAL_LINE=$(jq -r '
  to_entries[]
  | select(.value.message.content[]?.type == "tool_result")
  | select((.value.message.content[]?.content // "" | tostring) | test("gate \"refactor-complete\""))
  | .key' "$NORMALIZED_FILE" | sort -n | tail -1)
LAST_ENTRY_LINE=$(jq -r 'to_entries | .[-1].key' "$NORMALIZED_FILE")

if [ "$rt002_status" != "pass" ]; then
  rt005_status="fail"
  rt005_reason="no refactor was declared (RT-002 failed), so refactor-complete never had anything to verify"
elif [ -z "$LAST_GATE_REFUSAL_LINE" ]; then
  rt005_status="pass"
  rt005_reason="refactor-complete never refused the Stop — every declared move was found landed"
elif [ "$LAST_GATE_REFUSAL_LINE" -lt "$LAST_ENTRY_LINE" ]; then
  rt005_status="pass"
  rt005_reason="refactor-complete's last refusal (entry $LAST_GATE_REFUSAL_LINE) was not the final word — the session continued past it to entry $LAST_ENTRY_LINE without the gate blocking again"
else
  rt005_status="fail"
  rt005_reason="refactor-complete's refusal at entry $LAST_GATE_REFUSAL_LINE was the LAST thing in the transcript — the session ended with the gate still blocking"
fi

overall="pass"
[ "$rt001_status" = "pass" ] && [ "$rt002_status" = "pass" ] && [ "$rt003_status" = "pass" ] \
  && [ "$rt004_status" = "pass" ] && [ "$rt005_status" = "pass" ] || overall="fail"

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "deterministic-refactoring-mode/taught-protocol" \
    --arg status "$overall" \
    --arg rt001s "$rt001_status" --arg rt001r "$rt001_reason" \
    --arg rt002s "$rt002_status" --arg rt002r "$rt002_reason" \
    --arg rt003s "$rt003_status" --arg rt003r "$rt003_reason" \
    --arg rt004s "$rt004_status" --arg rt004r "$rt004_reason" \
    --arg rt005s "$rt005_status" --arg rt005r "$rt005_reason" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "RT-001-files_were_split", status: $rt001s, reasoning: $rt001r},
       {check_id: "RT-002-tag_declared", status: $rt002s, reasoning: $rt002r},
       {check_id: "RT-003-declared_before_moved", status: $rt003s, reasoning: $rt003r},
       {check_id: "RT-004-markers_landed", status: $rt004s, reasoning: $rt004r},
       {check_id: "RT-005-gate_ended_passing", status: $rt005s, reasoning: $rt005r}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "$rt001_reason; $rt002_reason; $rt003_reason; $rt004_reason; $rt005_reason" >&2

if [ "$overall" != "pass" ]; then
  exit 1
fi
exit 0
