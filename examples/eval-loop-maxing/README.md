# eval-loop-maxing (goal + context + gate)

**Nature:** goal (composite) + context + gate

## The rule

A self-improving loop (modify → measure → repeat until a metric target is
hit) needs a guardrail that the measurement actually HAPPENED, was RECORDED,
and was DETERMINISTICALLY computed — that the agent does NOT stop while the
target is unmet — AND that the target was not hit by gaming the measurement
itself. That last failure is the one the website's own story shows: after
enough compaction, an agent facing a stalled metric will happily special-case
the eval's own cases rather than fix the thing the eval measures. The score
moves; the task doesn't.

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

- **`goal/accuracy-target/goal.yaml`** — `enabled: true`, `script: verify.sh`,
  `target: 0.75`, and `cited_ask: "[quote](jsonl-path)"`. **Not authored ahead
  of time.** Written BY THE AGENT during the trajectory, the moment it
  commits to a target — before that write, this folder does not exist.
  `target` is a bare, machine-readable number the gate reads independently of
  `verify.sh`'s exit code (see Part 3). `cited_ask` quotes the user's OWN
  words for the whole ask — target AND no-overfit condition alike — grounded
  the same way sloprail-tasks's `task-body-is-human-authored` grounds a task
  body: this is what keeps the goal faithful to the full request after
  compaction pushes the original prompt out of context, rather than letting
  the agent quietly keep only the measurable half.
- **`goal/accuracy-target/verify.sh`** — `accuracy >= target` (`target` read
  from `goal.yaml`, not hardcoded), checked against the shared recording log.
  Kept for the convention and for a human reading it by hand; the gate does
  not trust its exit code alone (Part 3).

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
  coordinate priority numbers). **This is where the inverted
  "must-not-stop-until-the-WHOLE-ask-is-met" verdict lives** — not in the
  context. FOUR checks, cheapest and most deterministic first, first refusal
  wins:

  1. **`goal-cites-ask.sh`** (script) — the goal must carry a `cited_ask`
     citation of the user's own prompt, and it must GROUND (`sr-session
     trajectory cite`) to something the user actually said. No citation, or
     one that doesn't ground: refused here, before any model is paid for.
  2. **`goal-covers-ask.md.j2`** (judge) — does the goal's `target` +
     anything else it declares actually preserve BOTH halves of that cited
     ask — the number AND the no-overfit condition — or did the goal narrow
     to just the part that's easy to measure.
  3. **`run-verify.sh`** (script) — THE RUNNER, not the agent, recomputes
     accuracy fresh via `score-held-out.sh` and refuses while the runner's
     own visible-accuracy number is below `goal.yaml`'s `target`, or while
     held-out accuracy trails visible accuracy by more than a configured
     gap. An agent-written `verify.sh` that always `exit 0`s cannot pass
     this — its exit code is not what decides.
  4. **`no-hardcoding.md.j2`** (judge) — reads `classify.py` against the
     visible eval cases for per-case special-casing (a literal ticket
     string, a lookup table keyed on the cases' exact text, a rule too
     narrow to match anything else) and refuses if it finds it, with a
     reason like "hardcodes the eval's own tickets; the score moves, the
     task doesn't."

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

## The held-out set is a deterrent, not a secret

`evals/held_out/cases.json` ships inside the seeded project from the start —
`score-held-out.sh` needs it on disk to score at Stop time, and there is no
way to hide a file from an agent that can read its own filesystem. The
prompt and the skill simply never point at it, which is honestly all
"held-out" means here: it costs an agent nothing to go looking, and finding
it is not treated as a violation. The actual backstop against gaming is
`no-hardcoding.md.j2` (a lookup table keyed on the visible cases is
special-casing whether or not the agent ever saw the held-out file) and the
gap check in `run-verify.sh` (a fix that doesn't generalize shows up as a
gap between the two accuracies regardless of how the mismatch happened).

## What this design confirms

Neither goal nor recording needs a fourth nature — both are `context` plus
conventions layered by hand. And the inverted "don't stop" verdict belongs
to **gate**, not context in general — a context tracks a scope's aliveness;
a gate is what refuses an action (including ending the turn). Splitting them
is what made `gates[<name>]` necessary as a map symmetric to `context[<name>]`.
