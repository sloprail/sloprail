#!/bin/sh
# The guardrail apparatus (context/refactoring, file-guard/moved-content-
# reconciles, gate/refactor-complete) activates ONLY when the agent writes a
# `#refactor scope=<fqn>,...` tag — a syntax no skill in this repo teaches, and
# nothing in prompt.md mentions. This fixture asks for an ordinary split-this-
# file refactor with no hint of that convention, and scores whether the agent
# ever discovers it on its own.
#
# Three checks, each independent, over the whole transcript:
#   RT-001 files_were_split   — the agent actually did the refactor (the task
#                               itself succeeded, independent of the
#                               guardrail — the control: without this, "the
#                               guardrail never activated" could just mean
#                               the agent never did anything at all)
#   RT-002 tag_ever_declared  — a #refactor tag ever appeared anywhere in the
#                               transcript (the ONLY way the context can enter)
#   RT-003 markers_landed     — an sr:moved-from marker ever landed in either
#                               new file (the ONLY way the completeness gate
#                               or the file-guard's reconcile check has
#                               anything to check against)
#
# The expected, honest real-world result is RT-001 pass, RT-002/RT-003 fail —
# proving the guardrail is currently inert against an agent nobody has told
# about its convention. A pass here (the agent spontaneously inventing the
# exact `#refactor scope=path@sha:start-end` syntax) would be the surprising
# result worth investigating on its own.
#
# Verdict written to $SR_EVAL_VERDICT_OUT, pass or fail, whole-run —
# {subject, status, rows}, a10n-eval's own scorer verdict.json shape.
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

# --- RT-001: files_were_split ---
if [ -f "$SR_EVAL_PROJECT_DIR/payments/user_reads.go" ] && [ -f "$SR_EVAL_PROJECT_DIR/payments/user_writes.go" ]; then
  rt001_status="pass"
  rt001_reason="both payments/user_reads.go and payments/user_writes.go exist"
else
  rt001_status="fail"
  rt001_reason="the split was never completed — user_reads.go and/or user_writes.go do not exist, so this run says nothing about the guardrail either way"
fi

# --- RT-002: tag_ever_declared ---
NORMALIZED_FILE=$(mktemp)
trap 'rm -f "$NORMALIZED_FILE"' EXIT
"$SR_SESSION" trajectory normalize --path "$SR_EVAL_TRANSCRIPT" --whole-session --events PostTagWrite > "$NORMALIZED_FILE"

TAG_COUNT=$(jq '[.[] | .events[]? | select(.kind == "PostTagWrite") | .fields.tags[]? | select(.label == "refactor")] | length' "$NORMALIZED_FILE")

if [ "$TAG_COUNT" -gt 0 ]; then
  rt002_status="pass"
  rt002_reason="a #refactor tag appeared $TAG_COUNT time(s) in the transcript — the agent discovered or guessed the convention"
else
  rt002_status="fail"
  rt002_reason="no #refactor tag ever appeared — the refactoring context never had anything to activate on, so the whole guardrail apparatus (file-guard + gate) stayed inert for this entire run"
fi

# --- RT-003: markers_landed ---
MARKER_COUNT=0
for f in "$SR_EVAL_PROJECT_DIR/payments/user_reads.go" "$SR_EVAL_PROJECT_DIR/payments/user_writes.go"; do
  if [ -f "$f" ] && grep -q 'sr:moved-from' "$f" 2>/dev/null; then
    MARKER_COUNT=$((MARKER_COUNT + 1))
  fi
done

if [ "$MARKER_COUNT" -gt 0 ]; then
  rt003_status="pass"
  rt003_reason="an sr:moved-from marker landed in $MARKER_COUNT of the 2 split files"
else
  rt003_status="fail"
  rt003_reason="no sr:moved-from marker landed in either split file — nothing for the reconcile file-guard or the completeness gate to check, regardless of whether the move was actually byte-faithful"
fi

overall="pass"
[ "$rt001_status" = "pass" ] && [ "$rt002_status" = "pass" ] && [ "$rt003_status" = "pass" ] || overall="fail"

if [ -n "${SR_EVAL_VERDICT_OUT:-}" ]; then
  jq -n \
    --arg subject "deterministic-refactoring-mode/activation-gap" \
    --arg status "$overall" \
    --arg rt001s "$rt001_status" --arg rt001r "$rt001_reason" \
    --arg rt002s "$rt002_status" --arg rt002r "$rt002_reason" \
    --arg rt003s "$rt003_status" --arg rt003r "$rt003_reason" \
    '{subject: $subject, status: $status, rows: [
       {check_id: "RT-001-files_were_split", status: $rt001s, reasoning: $rt001r},
       {check_id: "RT-002-tag_ever_declared", status: $rt002s, reasoning: $rt002r},
       {check_id: "RT-003-markers_landed", status: $rt003s, reasoning: $rt003r}
     ]}' > "$SR_EVAL_VERDICT_OUT"
fi

echo "$rt001_reason; $rt002_reason; $rt003_reason" >&2

if [ "$overall" != "pass" ]; then
  exit 1
fi
exit 0
