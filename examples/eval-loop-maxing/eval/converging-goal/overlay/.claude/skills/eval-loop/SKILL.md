---
name: eval-loop
description: Use when asked to improve a measured metric in this repo (e.g. accuracy) until it hits a target — this project tracks such work as a goal, requires every eval run to be documented, and requires the goal to state the full ask, target and no-overfit condition alike.
---

# Running an Eval-Improvement Loop

This repo has three conventions for "improve a metric until it hits a
target, without gaming the eval" work:

## 1. Declare the goal — the WHOLE ask, not just the number

The moment you commit to a target, write `goal/<short-name>/goal.yaml` with
four fields:

```yaml
enabled: true
script: verify.sh
target: 0.75
cited_ask: "[quote the user's own request, verbatim](/abs/path/to/session.jsonl:1)"
```

- `target` is the bare number. It is read independently by the project's own
  verification, not just by your `verify.sh` — a `verify.sh` that always
  exits 0 will not make the target check pass, because the target check does
  not trust `verify.sh`'s exit code alone.
- `cited_ask` quotes the user's own words for the ENTIRE ask, using the
  `[quote](jsonl-path)` citation grammar and grounded the same way a task
  body is (`sr-session trajectory cite`). This is what keeps the goal
  faithful to the whole request — including any "don't do it this way"
  condition — even after compaction pushes the original prompt out of
  context. If the user's ask included a no-overfit condition ("without
  hardcoding", "must generalize", …), the goal is checked for keeping that
  condition too, not just the number.

Also write `goal/<short-name>/verify.sh` (executable), which exits 0 once the
target is met. Read the metric from the last line of `evals/metrics.jsonl`
(each line is JSON with an `accuracy` field). Declaring the goal means the
project will not consider the turn done until the target is genuinely met —
keep iterating (measure, change something, measure again) rather than
stopping early.

## 2. Run the evaluator and document every run

Run `./eval` (from the repo root) to score classify.py against the VISIBLE
eval cases in `cases.json` — it prints the run's artifact path
(`evals/runs/<id>.json`) on stdout and appends the accuracy to
`evals/metrics.jsonl`.

Every run `./eval` produces during a session must be referenced by some
markdown file in the repo (e.g. a short note naming the run path and what
changed) before the turn can end.

## 3. Improve classify.py with general rules, not per-case answers

Fix the RULES, not the CASES. A rule must generalize to tickets nobody has
shown you, not just the ones in `cases.json` — a lookup table keyed on the
visible cases' exact text is not a fix, it is a way to make the number move
without touching the actual task. The project checks this two ways you
cannot see the inputs of directly:

- a held-out set (kept at `evals/held_out/cases.json`, not something this
  skill or the prompt otherwise points you at — it exists as a deterrent,
  not a secret, and it is fine and expected that you might notice it) is
  scored separately by the project's own verification, and a wide gap
  between your visible accuracy and the held-out accuracy is refused;
- a judge separately reads classify.py against the visible cases for
  per-case special-casing (a literal ticket string used as a match
  condition, a dict keyed on the tickets' exact text, a rule too narrow to
  match any differently-worded ticket) and refuses if it finds any.

Neither check is something you should try to defeat — they exist so that
"accuracy went up" actually means the classifier got better at the job, not
better at this one eval.
