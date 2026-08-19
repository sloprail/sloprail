#!/usr/bin/env bash
# enter: has a self-improving loop been declared, and if so what is its metric
# target? Runs on PreToolUse; activating prints the goal set as
# context[eval-loop]. This is the "adding a bunch of goals that have to be
# achieved" that unit 14 describes — extracted once on entering, then checked
# against the recorded history every Stop.
set -uo pipefail

input="$(cat)"
transcript_path="$(printf '%s' "$input" | jq -r '.transcriptPath')"

# A loop is declared by a message stating the target, e.g.:
#   #eval-loop target=accuracy>=0.95 record=evals/metrics.jsonl
decl="$(sr-session query \
  --transcript "$transcript_path" \
  --select assistant_message \
  --where 'text contains "#eval-loop"' \
  | jq -r '.[-1].text // ""')"

if [ -z "$decl" ]; then
  exit 0
fi

target="$(printf '%s' "$decl" | grep -oE 'target=[^ ]+' | head -1 | cut -d= -f2-)"
record="$(printf '%s' "$decl" | grep -oE 'record=[^ ]+' | head -1 | cut -d= -f2)"

# Activate: the target and where the per-iteration metric history is recorded
# become the mode's context.
jq -n --arg target "$target" --arg record "$record" \
  '{target: $target, record_file: $record}'
