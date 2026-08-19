#!/usr/bin/env bash
# exit: is THIS context's goal met? Delegates to the goal's own verify.sh
# rather than re-implementing the check here — the goal is the thing that
# knows how to verify itself; this context is only the wiring that calls it
# on a Stop. This is the inverted verdict unit 14 needs: while unmet, the
# Stop is refused and the loop must keep going.
set -uo pipefail

input="$(cat)"
goal_name="$(printf '%s' "$input" | jq -r '.currentContext.payload.goal // ""')"

if [ -z "$goal_name" ]; then
  exit 0
fi

goal_dir="${SR_WORKSPACE:-.}/.sloprail/goal/$goal_name"
script_name="$(grep '^script:' "$goal_dir/goal.yaml" | awk '{print $2}')"
verify_script="$goal_dir/${script_name:-verify.sh}"

if [ ! -x "$verify_script" ]; then
  cat <<EOF
{"decision":"block","reason":"goal-tracking is active for '$goal_name' but its verify.sh is missing or not executable at $verify_script."}
EOF
  exit 1
fi

if "$verify_script"; then
  # Target met — this context deactivates, the Stop proceeds.
  exit 0
fi

cat <<EOF
{"decision":"block","reason":"Goal '$goal_name' target not yet met. Do not stop: keep iterating until verify.sh passes."}
EOF
exit 1
