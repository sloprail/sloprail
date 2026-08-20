# eval-loop-maxing (goal + context + gate)

**Unit:** [14_eval-loop-maxing](/Users/nsviridenko/ws/sloprail/strategy/memories/topics/20260812_no-slop/units/14_eval-loop-maxing/UNIT.md)
**Nature:** goal (composite) + context + gate, candidate list — rebuilt twice: 2026-08-19 split into goal+recording, then again same day after the context/gate reversal (decision slice 7)

## The rule

A self-improving loop (modify → measure → repeat until a metric target is
hit) needs a guardrail that the measurement actually HAPPENED, was RECORDED,
and was DETERMINISTICALLY computed — and that the agent does NOT stop while
the target is unmet.

## Three independent concerns, three pieces

The original build treated this as one context. Two re-reads later it's
three pieces, each with one job:

1. **The goal** (`goal/accuracy-target/`, a project-level sibling of
   `.sloprail/` — NOT under it) — what "achieved" means. A composite
   primitive the user maintains; the engine neither loads nor dispatches on
   `goal.yaml`. It is realised entirely through the context + gate below.
2. **A tracking context** (`context/goal-tracking/`) — is a goal currently
   active. Pure lifecycle, `{active, payload}`. Does **not** block anything.
3. **A verify gate** (`gate/goal-verify/`) — the thing that actually refuses
   a premature Stop, by reading the tracking context and running the goal's
   own verify script.

Separately, **recording** (`context/recording/`) is a fourth, independent
piece: every eval run must be documented, regardless of any goal.

## Part 1 — the goal, a composite primitive

Lives at project-level **`goal/`** — a sibling of `.sloprail/`, not inside
it. The engine never reads it: `goal` is not a sloprail primitive, so the
declaration loader does not scan for it. Only the context and gate below
touch it, by path, at `${SR_WORKSPACE}/goal/<name>/`.

- **`goal/accuracy-target/goal.yaml`** — `enabled: true`, `script: verify.sh`.
  **Not authored ahead of time.** Written BY THE AGENT during the trajectory,
  the moment it commits to a target — before that write, this folder does
  not exist. The target itself lives in `verify.sh`, not here (2026-08-19:
  "замени target на script, чтобы на verify.sh ссылаться") — one place for
  the condition.
- **`goal/accuracy-target/verify.sh`** — `accuracy >= 0.95`, checked against
  the shared recording log.

## Part 2 — goal-tracking, pure lifecycle (reversed 2026-08-19)

- **`context/goal-tracking/`** — wakes on `PostFileWrite` matched to any
  `goal/*/goal.yaml` (Post, not Pre: a goal's `enabled` only exists once the
  write settles). `enter` reads it and activates only if enabled.
  **`exit` does NOT run verify and does NOT refuse the Stop** — it only
  reads `gates["goal-verify"].status`, the paired gate's own verdict, the
  `gates` map being symmetric to `context` (his: "контекст может иметь
  доступ ко всем гейтам, так же, как гейты — к контексту"). `pass` →
  deactivate; anything else → stay active.

**Why the reversal.** The first build had this context's own `exit` both
deciding "can we stop" and calling `verify.sh` — thick, doing two jobs. His
question forced the split: *"должен ли exit контекста и запрещать
останавливаться, и параллельно помечать, что он вышел? Это две разные
вещи."*

## Part 3 — goal-verify, the gate that actually blocks

- **`gate/goal-verify/`** — `on: [{event: Stop}]`,
  `require: [{context: goal-tracking}]` (dependency, not priority — chosen
  because a marketplace of independent plugins has no single party to
  coordinate priority numbers), `checks: [{script: run-verify.sh}]`.
  `run-verify.sh` calls the active goal's `verify.sh` and refuses the Stop
  when it fails. **This is where unit 14's inverted "must-not-stop-until-
  target" verdict actually lives now** — not in the context.

## Part 4 — recording, unrelated to the goal

- **`context/recording/`** — wakes on `PreCommandInvoke` matched to an eval
  command, re-enters on every eval invocation. `exit` is a **completeness**
  check (a plain script): every eval run the trajectory has produced so far
  must have a markdown documenting it, found by matching the run path a
  `dummy-eval.sh`-shaped command prints on its own stdout — proof a run
  happened and where, not a guess.

## What this rebuild confirmed twice over

First rebuild: neither goal nor recording needed a fourth nature — both are
`context` plus conventions layered by hand. Second rebuild: the inverted
"don't stop" verdict that originally broke the single `on→check` structure
belongs to **gate**, specifically, not to context in general — a context
tracks a scope's aliveness; a gate is what refuses an action (including
ending the turn). Splitting them cleanly is what made `gates[<name>]`
necessary as a map symmetric to `context[<name>]`.
