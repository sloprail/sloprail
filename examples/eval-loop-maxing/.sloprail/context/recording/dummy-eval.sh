#!/usr/bin/env bash
# A stand-in for a real eval command — not part of the guardrail itself, but
# what a real one looks like for this example to check something concrete
# against. Produces a run artifact (a JSON hashmap of scores) and prints its
# path on stdout, so the trajectory's tool_use output is proof this run
# happened and where it lives — no guessing which file a run "must have"
# produced.
set -uo pipefail

mkdir -p evals/runs
run_id="run-$(date +%s 2>/dev/null || echo unknown)-$$"
run_path="evals/runs/${run_id}.json"

cat > "$run_path" <<EOF
{
  "run_id": "$run_id",
  "scores": {
    "accuracy": 0.91,
    "latency_ms": 420
  }
}
EOF

echo "$run_path"
