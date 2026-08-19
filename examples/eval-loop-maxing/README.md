# eval-loop-maxing (goal + two contexts)

**Unit:** [14_eval-loop-maxing](/Users/nsviridenko/ws/sloprail/strategy/memories/topics/20260812_no-slop/units/14_eval-loop-maxing/UNIT.md)
**Nature:** goal (composite, see decision 20260818_no-slop-primitives, slice 6) + context, candidate list — rebuilt 2026-08-19 from a single `mode` after his re-read

## The rule

A self-improving loop (modify → measure → repeat until a metric target is
hit) needs a guardrail that the measurement actually HAPPENED, was RECORDED,
and was DETERMINISTICALLY computed — and that the agent does NOT stop while
the target is unmet.

## Why this is TWO things, not one context

The original build treated this as a single context with one exit script
checking three things at once. Re-reading it split cleanly into two
independent concerns, each with its own lifecycle (his, 2026-08-19):

1. **A goal being achieved** — "a certain role that has to be achieved... one
   loop, one context. You enter and exit it." Finite: it exits once the
   target is met.
2. **Every run being recorded** — "making sure that every evaluation run is
   recorded somewhere... never exits, because there may be new evaluation
   runs." Not finite in the same way: it can open and close many times a
   session, once per run, and never permanently closes.

Splitting them means each `exit` answers one question, and the two can be
reused independently — a goal without the recording discipline, or recording
discipline for a run that has no target attached.

## Part 1 — the goal, a composite primitive

`goal` is NOT a fourth low-level nature. It is specced in TypeSpec
(`GoalDeclaration`/`GoalVerifyScript`) but the engine does not wire it into a
context automatically — a goal is realised by PAIRING a `goal.yaml` with an
ordinary `context` that reads it. Two things happen only because this example
builds both halves by hand.

- **`goal/accuracy-target/goal.yaml`** — `enabled: true`, `script: verify.sh`.
  **Not authored ahead of time.** This file is written BY THE AGENT during
  the trajectory, the moment it commits to a target — before that write,
  `goal/accuracy-target/` does not exist at all.
- **`goal/accuracy-target/verify.sh`** — the target lives HERE (`accuracy >=
  0.95`), not duplicated as prose in `goal.yaml` — one place for the
  condition, per his correction ("замени target на script, чтобы на verify.sh
  ссылаться").
- **`context/goal-tracking/`** — the general wiring EVERY goal needs, not
  specific to this one. `on: [{event: PostFileWrite, match: path startsWith
  "goal/" and path endsWith "goal.yaml"}]` — Post, not Pre, because a goal's
  `enabled` flag only exists once the write has SETTLED (a Pre cannot always
  predict the write at all). `enter` reads the settled content and activates
  only if `enabled`; `exit` calls the goal's own `script` (resolved from
  `goal.yaml`, not hardcoded) — the inverted verdict lives here: while unmet,
  the Stop is refused.

## Part 2 — recording, a context that keeps reopening

- **`context/recording/`** — wakes on `PreCommandInvoke` matched to an eval
  command. `enter` activates with no payload; `exit` is a **completeness**
  check (the primitive, realised as a plain script per his "начать с того,
  что это просто скрипты"): every eval run this trajectory has produced so
  far must have a markdown file whose frontmatter references its run path.
  Refuses the Stop and re-enters on the next eval invocation until every run
  is documented — which is what "never really exits" means in practice: it
  keeps coming back, not that it stays permanently active.
- **`dummy-eval.sh`** — a stand-in for a real eval command, not part of the
  guardrail. Produces a run artifact (`evals/runs/{id}.json`, a hashmap of
  scores) and prints its path on stdout — the trajectory's own tool_use
  output is proof the run happened and where it lives, so `exit` never has to
  guess which file a run "must have" produced.

## What this rebuild confirmed

Neither half needed a fourth nature. The goal is `context` + a small
higher-level file convention layered on top by hand; recording is an ordinary
context that simply re-enters more than once per session. The inverted
"don't stop" verdict from the original single-context build is still exactly
a context's `exit` refusing a Stop — now scoped to the one concern (the
target) it actually belongs to.
