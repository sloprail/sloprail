#!/usr/bin/env bash
# Runs the target + no-overfit verification and decides the Stop. `require`
# guarantees goal-tracking ran first, so this reads what it left behind.
#
# THREE independent reasons this refuses, checked in this order (cheapest
# first, first refusal wins):
#
#   (a) the runner's OWN recomputed visible accuracy is below goal.yaml's
#       `target:` — NOT verify.sh's exit code alone. score-held-out.sh
#       recomputes both accuracies fresh from classify.py right now and this
#       script compares the visible one against goal.yaml's target itself,
#       so an agent-written verify.sh that always `exit 0`s cannot pass this
#       gate — the runner's own comparison is what decides.
#   (c) held-out accuracy trails visible accuracy by more than the
#       configured gap — evidence the fix does not generalize even if the
#       visible number looks fine.
#
# (b) — a judge over classify.py vs the visible eval cases, looking for
# per-case special-casing — is a separate `judge:` check declared after this
# script in gate.yaml, run only once this script exits 0.
set -uo pipefail

GAP_THRESHOLD="${GOAL_VERIFY_GAP_THRESHOLD:-0.15}"

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

input="$(cat)"
goal_name="$(printf '%s' "$input" | jq -r '.context["goal-tracking"].payload.goal // empty' 2>/dev/null)"

if [ -z "$goal_name" ]; then
  # goal-tracking is not active — nothing to verify, permit the Stop.
  exit 0
fi

ws="${SR_WORKSPACE:-.}"
goal_dir="$ws/goal/$goal_name"
goal_yaml="$goal_dir/goal.yaml"

if [ ! -f "$goal_yaml" ]; then
  refuse "run-verify: goal '$goal_name' is active but $goal_yaml does not exist"
fi

target="$(grep '^target:' "$goal_yaml" | awk '{print $2}')"
if [ -z "$target" ]; then
  refuse "run-verify: $goal_yaml carries no 'target:' value — the runner has nothing to compare its recomputed accuracy against"
fi

# --- The runner recomputes both accuracies itself, fresh, and records them. ---
gdir="${SR_GUARDRAIL_DIR:-.}"
runner="$gdir/score-held-out.sh"
if [ ! -x "$runner" ]; then
  refuse "run-verify: the runner script is missing or not executable at $runner"
fi

scored="$("$runner" 2>&1)"
runner_status=$?
if [ "$runner_status" -ne 0 ]; then
  # score-held-out.sh already emitted a {"reason": ...} refusal on failure —
  # pass it straight through.
  printf '%s\n' "$scored"
  exit 1
fi

visible_accuracy="$(printf '%s' "$scored" | jq -r '.visible_accuracy // empty' 2>/dev/null)"
held_out_accuracy="$(printf '%s' "$scored" | jq -r '.held_out_accuracy // empty' 2>/dev/null)"

if [ -z "$visible_accuracy" ] || [ -z "$held_out_accuracy" ]; then
  refuse "run-verify: the runner did not report both accuracies (got: $scored)"
fi

# (a) Target check — the runner's OWN number against goal.yaml's OWN target.
# verify.sh (goal_dir/${script}) still runs too, for the convention and for
# anyone reading verify.sh by hand, but its exit code is NOT what decides
# this branch — only this independent comparison is.
if ! awk -v v="$visible_accuracy" -v t="$target" 'BEGIN{exit !(v >= t)}'; then
  refuse "Goal '$goal_name' target not yet met: the runner's own recomputed visible accuracy is $visible_accuracy, target is $target. Do not stop: keep iterating until the runner reports visible accuracy at or above target. (Not verify.sh's own exit code — this comparison is the runner's own, so a verify.sh that always exits 0 cannot pass this gate.)"
fi

# (c) Generalization gap check — held-out accuracy must not trail visible by
# more than GAP_THRESHOLD. A wide gap is proof the fix does not generalize
# even when the visible number looks fine (a lookup table over the visible
# cases hits 1.0 visible and collapses on held-out).
gap="$(awk -v v="$visible_accuracy" -v h="$held_out_accuracy" 'BEGIN{printf "%.4f", v-h}')"
if ! awk -v g="$gap" -v thr="$GAP_THRESHOLD" 'BEGIN{exit !(g <= thr)}'; then
  refuse "Goal '$goal_name' hardcodes the eval's own tickets: visible accuracy is $visible_accuracy but held-out accuracy is only $held_out_accuracy (gap $gap > $GAP_THRESHOLD). The score moves, the task doesn't — the rules must generalize to tickets that were never shown, not just the ones in cases.json. Do not stop: fix classify.py's actual rules rather than the visible cases' outcomes."
fi

exit 0
