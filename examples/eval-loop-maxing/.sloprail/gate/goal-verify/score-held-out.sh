#!/usr/bin/env bash
# THE RUNNER, not the agent. Recomputes accuracy against BOTH the visible
# eval cases (cases.json) and the held-out set (evals/held_out/cases.json),
# fresh, from whatever classify.py looks like right now, and appends a
# combined row to evals/metrics.jsonl itself.
#
# Two things this buys, deliberately:
#
#   1. The agent's own ./eval only ever recorded VISIBLE accuracy (seed/eval,
#      the agent-facing tool). Held-out accuracy has never been computed
#      before this point in the trajectory — this script is the first and
#      only thing that runs classify.py against evals/held_out/cases.json,
#      and it is the engine's own check, not something the agent invoked.
#   2. Visible accuracy is RECOMPUTED here too, rather than trusted from the
#      agent's last ./eval run — so a goal-verify decision never rests on a
#      number the agent could have last written before quietly reverting
#      classify.py, or on a verify.sh the agent controls. Both numbers this
#      gate acts on came from the runner's own invocation of classify.py,
#      just now.
#
# held_out/cases.json is not a secret — the README and the skill say plainly
# that it exists and where, as a deterrent rather than a trap: an agent that
# goes looking will find it. The guardrail's real backstop against gaming is
# the no-hardcoding judge (a lookup keyed on visible cases' exact text would
# still be visible as special-casing, whether or not the held-out file was
# ever read) and this gap check, which fires regardless of how the mismatch
# happened.
set -uo pipefail

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

ws="${SR_WORKSPACE:-.}"
classify_dir="$ws"
cases_visible="$ws/cases.json"
cases_held_out="$ws/evals/held_out/cases.json"
metrics="$ws/evals/metrics.jsonl"

if [ ! -f "$classify_dir/classify.py" ]; then
  refuse "score-held-out: no classify.py found at $classify_dir — nothing to score"
fi
if [ ! -f "$cases_visible" ]; then
  refuse "score-held-out: visible eval cases not found at $cases_visible"
fi
if [ ! -f "$cases_held_out" ]; then
  refuse "score-held-out: held-out eval cases not found at $cases_held_out — the deterrent set must ship with the project"
fi

score_one() {
  # score_one <cases-file>  ->  prints "<correct> <total>" on stdout.
  python3 - "$classify_dir" "$1" <<'PYEOF'
import importlib.util
import json
import sys

classify_dir, cases_path = sys.argv[1], sys.argv[2]

spec = importlib.util.spec_from_file_location("classify", classify_dir + "/classify.py")
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

with open(cases_path) as f:
    cases = json.load(f)

correct = 0
for case in cases:
    result = mod.classify(case["text"])
    intent = result[0] if isinstance(result, (tuple, list)) else result
    if intent == case["intent"]:
        correct += 1

print(correct, len(cases))
PYEOF
}

visible_result="$(score_one "$cases_visible" 2>&1)" || refuse "score-held-out: classify.py raised an error scoring the visible cases: $visible_result"
held_out_result="$(score_one "$cases_held_out" 2>&1)" || refuse "score-held-out: classify.py raised an error scoring the held-out cases: $held_out_result"

visible_correct="$(printf '%s' "$visible_result" | awk '{print $1}')"
visible_total="$(printf '%s' "$visible_result" | awk '{print $2}')"
held_out_correct="$(printf '%s' "$held_out_result" | awk '{print $1}')"
held_out_total="$(printf '%s' "$held_out_result" | awk '{print $2}')"

visible_accuracy="$(awk -v c="$visible_correct" -v t="$visible_total" 'BEGIN{printf "%.4f", c/t}')"
held_out_accuracy="$(awk -v c="$held_out_correct" -v t="$held_out_total" 'BEGIN{printf "%.4f", c/t}')"

mkdir -p "$ws/evals"
run_id="runner-$(date +%s 2>/dev/null || echo unknown)-$$"
jq -n --arg run_id "$run_id" --arg acc "$visible_accuracy" --arg ho "$held_out_accuracy" \
  '{run_id: $run_id, accuracy: ($acc | tonumber), held_out_accuracy: ($ho | tonumber), source: "runner"}' \
  >> "$metrics"

# Hand the values downstream on stdout for run-verify.sh to read, so it does
# not have to re-derive them by re-reading the file it just watched this
# script append to.
jq -n --arg acc "$visible_accuracy" --arg ho "$held_out_accuracy" \
  '{visible_accuracy: ($acc | tonumber), held_out_accuracy: ($ho | tonumber)}'
