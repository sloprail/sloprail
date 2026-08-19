#!/usr/bin/env bash
# exit: is the loop's target met? Consulted on a Stop while the mode is active.
# This is the INVERTED verdict that makes unit 14 different from every
# file-guard: it does not block a bad write, it blocks a premature STOP. Until
# the metric target is reached, "done" is false and the Stop is refused — the
# guard keeps the loop running rather than letting the agent quit early
# claiming success.
#
# The mode spans ITERATIONS: the check reads the recorded metric history, not
# one turn's output, to know whether progress is real and the target is met.
set -uo pipefail

input="$(cat)"
target="$(printf '%s' "$input" | jq -r '.currentContext.target // ""')"
record="$(printf '%s' "$input" | jq -r '.currentContext.record_file // ""')"

block() {
  cat <<EOF
{"decision":"block","reason":"$1"}
EOF
  exit 1
}

if [ -z "$target" ]; then
  # No target declared — nothing to hold the loop open against.
  exit 0
fi

# 1. The measurement was RECORDED — an artifact per iteration must exist.
if [ -z "$record" ] || [ ! -f "$record" ]; then
  block "eval-loop is active but no recorded metric history exists at '${record:-<unset>}'. Every iteration must record its measured metric before the loop can end; a loop with no trace cannot be trusted to have measured anything."
fi

# 2. The metric was DETERMINISTICALLY computed and actually HAPPENED — the last
# iteration's row must carry a real computed number, not a model-estimated one.
last="$(tail -1 "$record")"
value="$(printf '%s' "$last" | jq -r '.value // empty' 2>/dev/null)"
method="$(printf '%s' "$last" | jq -r '.method // empty' 2>/dev/null)"

if [ -z "$value" ]; then
  block "The latest row in '$record' carries no measured value — a recorded iteration must contain the actual computed metric, not a claim that it improved."
fi
if [ "$method" = "estimated" ] || [ "$method" = "model" ]; then
  block "The latest metric in '$record' was '$method'-derived, not deterministically computed. A vibed score is exactly what this guard exists to reject; compute the metric."
fi

# 3. Target reached? Parse "accuracy>=0.95" shape: <name><op><threshold>.
op="$(printf '%s' "$target" | grep -oE '(>=|<=|>|<|==)')"
threshold="$(printf '%s' "$target" | sed -E 's/.*(>=|<=|>|<|==)//')"

met=0
case "$op" in
  ">=") awk "BEGIN{exit !($value >= $threshold)}" && met=1 ;;
  "<=") awk "BEGIN{exit !($value <= $threshold)}" && met=1 ;;
  ">")  awk "BEGIN{exit !($value >  $threshold)}" && met=1 ;;
  "<")  awk "BEGIN{exit !($value <  $threshold)}" && met=1 ;;
  "==") awk "BEGIN{exit !($value == $threshold)}" && met=1 ;;
esac

if [ "$met" -eq 1 ]; then
  # Target reached — the mode deactivates and the Stop proceeds.
  exit 0
fi

# Not there yet — refuse the Stop. The loop must keep going.
block "eval-loop target '$target' not met (latest measured value: $value). Do not stop: keep iterating until the metric reaches its target. This is the loop staying trustworthy rather than ending on an unmet goal."
