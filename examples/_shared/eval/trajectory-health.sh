#!/bin/sh
# Shared trajectory-health judge, sourced by an example fixture's own score.sh.
#
# The plan-2026-09-24 shift: an eval no longer scores "did the guardrail catch
# the violation" as the primary signal — a model figuring out the right thing
# on its own, guardrail or not, is ALSO a fine outcome. What actually matters is
# whether the whole run's TRAJECTORY looks healthy: no stuck retry loops, no
# guardrail that never resolves, no wandering. Whether the guardrail fired is
# still recorded as a secondary, informational signal — useful for the
# analysis, not the pass/fail bar by itself.
#
# Usage, from a fixture's own score.sh at examples/<name>/eval/<fixture>/score.sh
# (after the usual SR_EVAL_* env checks):
#
#   . "$(dirname "$0")/../../../_shared/eval/trajectory-health.sh"
#   trajectory_health_check "$SCENARIO_DESCRIPTION" "$GUARDRAIL_DESCRIPTION"
#   # sets: TH_STATUS (pass/fail), TH_REASON (string)
#
# Requires SR_EVAL_TRANSCRIPT and a `sr-agent` on PATH (SR_EVAL_BIN_DIR is
# prepended to PATH by the caller, matching every other fixture's convention).
#
# The judge model is fixed to size-sm here deliberately — a cheap, fast model
# is the right instrument for "does this look stuck", the same reasoning
# eval-loop-maxing and the other guardrail judges already use for their own
# tier of question; a trajectory-health review is not a hard judgment call
# that needs a frontier model.
TRAJECTORY_HEALTH_MODEL="${TRAJECTORY_HEALTH_MODEL:-size-sm}"

trajectory_health_check() {
  scenario_desc="$1"
  guardrail_desc="$2"

  if [ -z "${SR_EVAL_TRANSCRIPT:-}" ]; then
    TH_STATUS="fail"
    TH_REASON="SR_EVAL_TRANSCRIPT not set — cannot review a trajectory that was never located"
    return
  fi
  if [ ! -f "$SR_EVAL_TRANSCRIPT" ]; then
    TH_STATUS="fail"
    TH_REASON="the transcript file $SR_EVAL_TRANSCRIPT does not exist"
    return
  fi

  template="$(dirname "$0")/../../../_shared/eval/trajectory-health.md"
  if [ ! -f "$template" ]; then
    TH_STATUS="fail"
    TH_REASON="trajectory-health.md not found beside this script at $template"
    return
  fi
  condense_jq="$(dirname "$0")/../../../_shared/eval/condense-transcript.jq"
  if [ ! -f "$condense_jq" ]; then
    TH_STATUS="fail"
    TH_REASON="condense-transcript.jq not found beside this script at $condense_jq"
    return
  fi

  # The RAW transcript is too large to hand a judge directly — measured on a
  # real ~14k-line-repo fixture run at ~600KB / ~230K tokens, well past a
  # judge model's context window ("Prompt is too long"). Condensed to a
  # compact, readable narrative (USER/ASSISTANT/TOOL_USE/TOOL_RESULT lines,
  # each truncated) via condense-transcript.jq — this is what a person
  # skimming for "did this look stuck" would actually want to read, not the
  # raw JSONL with every cache/token/attachment field repeated per entry.
  condensed_file=$(mktemp)
  jq -r -f "$condense_jq" "$SR_EVAL_TRANSCRIPT" > "$condensed_file" 2>/dev/null
  if [ ! -s "$condensed_file" ]; then
    TH_STATUS="fail"
    TH_REASON="condensing the transcript produced no output — the transcript may be malformed or condense-transcript.jq may need updating for this transcript's shape"
    rm -f "$condensed_file"
    return
  fi
  # A hard cap as a last-resort safety net beyond condensing — a pathological
  # single tool result (e.g. a test suite dumping thousands of lines despite
  # the per-line truncation above) could still blow the budget. 60000 chars
  # is comfortably under any judge model's context at the truncation lengths
  # condense-transcript.jq already applies.
  condensed_text="$(head -c 60000 "$condensed_file")"
  rm -f "$condensed_file"

  # The transcript is an AGENT'S OWN OUTPUT wrapped in <transcript> tags in the
  # template so the judge can tell reviewed content apart from its own
  # instructions (defense against a transcript that quotes or invents text
  # aimed at the judge). A transcript containing a literal "</transcript>"
  # could otherwise forge that boundary and inject text the judge would read
  # as outside the reviewed content — neutralize it before substitution.
  condensed_text="$(printf '%s' "$condensed_text" | sed 's#</transcript>#< /transcript>#g')"

  # Simple placeholder substitution, not a real template engine: each
  # placeholder is replaced by the CONTENTS OF A FILE, not an awk -v string —
  # awk -v cannot hold a value with an embedded newline (scenario_desc,
  # guardrail_desc, and the condensed transcript are all multi-line, and this
  # was measured to fail with "awk: newline in string" before switching to
  # files). Three inputs, three temp files, one substitution pass.
  prompt_file=$(mktemp)
  scenario_file=$(mktemp)
  guardrail_file=$(mktemp)
  transcript_file=$(mktemp)
  printf '%s' "$scenario_desc" > "$scenario_file"
  printf '%s' "$guardrail_desc" > "$guardrail_file"
  printf '%s' "$condensed_text" > "$transcript_file"

  awk -v scenario_file="$scenario_file" -v guardrail_file="$guardrail_file" -v transcript_file="$transcript_file" '
    function inject(file,    tline) {
      while ((getline tline < file) > 0) print tline
      close(file)
    }
    {
      if ($0 ~ /\{\{ SCENARIO_DESCRIPTION \}\}/) { inject(scenario_file); next }
      if ($0 ~ /\{\{ GUARDRAIL_DESCRIPTION \}\}/) { inject(guardrail_file); next }
      if ($0 ~ /\{\{ TRANSCRIPT_TEXT \}\}/) { inject(transcript_file); next }
      print
    }
  ' "$template" > "$prompt_file"

  # Run from a fresh, empty temp directory with tools restricted to something
  # that grants no filesystem/shell access — measured necessary: an
  # UNRESTRICTED first version (no --allowed-tools at all) ran in whatever
  # the calling score.sh's own CWD happened to be, and the judge agent used
  # its Bash access to run `git status` there, saw unrelated local repo
  # changes, and derailed into trying to fix them instead of answering the
  # health question it was asked. Passing an EMPTY string does not fix this
  # — sr-agent's ParseAllowedTools("") returns nil, which means no
  # --allowed-tools flag is passed at all and Claude Code's own default
  # tool access applies (confirmed to include Bash). "WebSearch" is granted
  # here as a harmless, sandboxed no-op tool the judge will never actually
  # need or call (the transcript is already inlined in the prompt) — it
  # exists only so --allowed-tools is a real, non-empty ALLOWLIST that
  # excludes Bash/Read/Write/Edit entirely, rather than an unset flag.
  judge_cwd=$(mktemp -d)
  raw="$(cd "$judge_cwd" && sr-agent --model "$TRAJECTORY_HEALTH_MODEL" --allowed-tools "WebSearch" --prompt "$(cat "$prompt_file")" 2>&1)"
  rm -f "$prompt_file" "$scenario_file" "$guardrail_file" "$transcript_file"
  rmdir "$judge_cwd" 2>/dev/null || true

  json="$(printf '%s' "$raw" | tr -d '\r' | sed 's/```json//g; s/```//g' | tr '\n' ' ' | grep -o '{[^{}]*}' | head -1)"
  if [ -z "$json" ]; then
    TH_STATUS="fail"
    TH_REASON="the trajectory-health judge did not produce a JSON verdict — raw output: $(printf '%s' "$raw" | head -c 500)"
    return
  fi

  healthy="$(printf '%s' "$json" | jq -r '.healthy' 2>/dev/null)"
  reasoning="$(printf '%s' "$json" | jq -r '.reasoning // ""' 2>/dev/null)"

  if [ "$healthy" = "true" ]; then
    TH_STATUS="pass"
    TH_REASON="${reasoning:-trajectory looked healthy}"
  elif [ "$healthy" = "false" ]; then
    TH_STATUS="fail"
    TH_REASON="$reasoning"
  else
    TH_STATUS="fail"
    TH_REASON="the judge's \"healthy\" field was not a boolean — raw verdict: $json"
  fi
}

# guardrail_fired_check: a purely INFORMATIONAL signal (never gates the
# overall verdict on its own under the new plan) — did the named guardrail
# ever refuse anything in this transcript at all. Greps the transcript's own
# tool_result content for the guardrail's attribution string, the same
# `gate "<name>"` / `file-guard "<name>"` text nature_*.go appends to every
# refusal.
#
# The quote before/after the name may be a literal `"` or a JSON-escaped
# `\"` — which one appears depends on how many times the refusal text itself
# got JSON-encoded before landing in the transcript (a raw hook stdout write
# vs. text nested inside a tool_result's own JSON string), and BOTH shapes
# were measured in real transcripts from real runs. `\{0,1\}` (POSIX basic
# regex; `?` is not portable to every grep) makes the backslash optional on
# both sides so either shape matches.
#
# Usage: guardrail_fired_check '<name>' ; # sets GF_STATUS (fired/never-fired), GF_COUNT
guardrail_fired_check() {
  name="$1"
  count=0
  if [ -f "${SR_EVAL_TRANSCRIPT:-/nonexistent}" ]; then
    count="$(grep -o "\\\\\{0,1\}\"$name\\\\\{0,1\}\"" "$SR_EVAL_TRANSCRIPT" | wc -l | tr -d ' ')"
  fi
  GF_COUNT="$count"
  if [ "$count" -gt 0 ]; then
    GF_STATUS="fired"
  else
    GF_STATUS="never-fired"
  fi
}
