---
name: eval-loop
description: Use when asked to improve a measured metric in this repo (e.g. accuracy) until it hits a target — this project tracks such work as a goal and requires every eval run to be documented.
---

# Running an Eval-Improvement Loop

This repo has two conventions for "improve a metric until it hits a
target" work:

## 1. Declare the goal

The moment you commit to a target, write
`goal/<short-name>/goal.yaml`:

```
enabled: true
script: verify.sh
```

and `goal/<short-name>/verify.sh` (executable), which exits 0 once the
target is met and non-zero otherwise. Read the metric from the last line
of `evals/metrics.jsonl` (each line is JSON with an `accuracy` field).

Declaring the goal means the project will not consider the turn done
until `verify.sh` passes — keep iterating (measure, change something,
measure again) rather than stopping early.

## 2. Run the evaluator and document every run

Run `./eval` (from the repo root) to score the current code and record
a result — it prints the run's artifact path (`evals/runs/<id>.json`) on
stdout and appends the accuracy to `evals/metrics.jsonl`.

Every run `./eval` produces during a session must be referenced by some
markdown file in the repo (e.g. a short note naming the run path and what
changed) before the turn can end.
