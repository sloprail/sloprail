# eval-loop-maxing (goal + context + gate)

**Nature:** goal (composite) + context + gate

A self-improving loop needs proof its measurement actually happened, was
recorded, and was computed deterministically — and that the agent doesn't
stop while its target is still unmet.

## Install

Make sure sloprail is installed — see the docs
[install page](https://sloprail.com/docs/getting-started/install/).

Then copy this example into your project:

```bash
git clone --depth 1 https://github.com/sloprail/sloprail /tmp/sloprail-clone && mkdir -p .sloprail && cp -R /tmp/sloprail-clone/examples/eval-loop-maxing/.sloprail/. .sloprail/ && rm -rf /tmp/sloprail-clone
```

Then try to stop the loop before the target is met, and confirm you see the
refusal below.

## The rule

A self-improving loop (modify → measure → repeat until a metric target is
hit) needs a guardrail that the measurement actually HAPPENED, was RECORDED,
and was DETERMINISTICALLY computed — and that the agent does NOT stop while
the target is unmet.

## Three independent concerns, three pieces

Each piece has one job:

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
  not exist. The target itself lives in `verify.sh`, not here — one place for
  the condition.
- **`goal/accuracy-target/verify.sh`** — `accuracy >= 0.95`, checked against
  the shared recording log.

## Part 2 — goal-tracking, pure lifecycle

- **`context/goal-tracking/`** — wakes on `PostFileWrite` matched to any
  `goal/*/goal.yaml` (Post, not Pre: a goal's `enabled` only exists once the
  write settles). `enter` reads it and activates only if enabled.
  **`exit` does NOT run verify and does NOT refuse the Stop** — it only
  reads `gates["goal-verify"].status`, the paired gate's own verdict, the
  `gates` map being symmetric to `context`. `pass` → deactivate; anything
  else → stay active.

The first build had this context's own `exit` both deciding "can we stop"
and calling `verify.sh` — thick, doing two jobs. The split separates
tracking (context) from refusing (gate).

## Part 3 — goal-verify, the gate that actually blocks

- **`gate/goal-verify/`** — `on: [{event: Stop}]`,
  `require: [{context: goal-tracking}]` (dependency, not priority — chosen
  because a marketplace of independent plugins has no single party to
  coordinate priority numbers), `checks: [{script: run-verify.sh}]`.
  `run-verify.sh` calls the active goal's `verify.sh` and refuses the Stop
  when it fails. **This is where the inverted "must-not-stop-until-target"
  verdict lives** — not in the context.

## Part 4 — recording, unrelated to the goal

- **`context/recording/`** — wakes on `PreCommandInvoke` matched to an eval
  command, re-enters on every eval invocation. Pure lifecycle, same split as
  Parts 2/3: `exit` does not run the completeness check itself, only reflects
  `gates["recording-verify"].status`.
- **`gate/recording-verify/`** — `on: [{event: Stop}]`,
  `require: [{context: recording}]`. Its check is the **completeness** check: every
  eval run the trajectory has produced so far must have a markdown documenting
  it, found by matching the run path a `dummy-eval.sh`-shaped command prints on
  its own stdout — proof a run happened and where, not a guess.

## What this design confirms

Neither goal nor recording needs a fourth nature — both are `context` plus
conventions layered by hand. And the inverted "don't stop" verdict belongs
to **gate**, not context in general — a context tracks a scope's aliveness;
a gate is what refuses an action (including ending the turn). Splitting them
is what made `gates[<name>]` necessary as a map symmetric to `context[<name>]`.
