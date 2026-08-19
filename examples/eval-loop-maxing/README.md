# eval-loop-maxing (mode)

**Unit:** [14_eval-loop-maxing](/Users/nsviridenko/ws/sloprail/strategy/memories/topics/20260812_no-slop/units/14_eval-loop-maxing/UNIT.md)
**Nature:** mode ([decision 20260818_no-slop-primitives](/Users/nsviridenko/ws/sloprail/strategy/memories/decisions/20260818_no-slop-primitives/DECISION.md), slice 4, candidate list)

## The rule

A self-improving loop (modify → measure → repeat until a metric target is hit)
needs a guardrail that the measurement actually HAPPENED, was RECORDED (an
artifact per iteration), and was DETERMINISTICALLY computed — and that the
agent does NOT stop while the target is unmet. Loop-maxing = maximizing the
loop's duration = maximizing autonomy; this is what makes long autonomy
trustworthy instead of drift.

## Why this is the hardest case, and why it's a mode

This is the unit that broke the single `on → check` structure, and it's the
real stress test for the mode nature. Three things make it unlike every
file-guard:

1. **The verdict is inverted.** Every file-guard blocks a bad *write*. This
   blocks a premature *Stop*. Same binary refusal, opposite trigger: "you are
   stopping while the contract is unsatisfied," not "what you produced is
   wrong." In the mode nature this needs no special machinery — it IS the exit
   condition. While the mode is active, `exit.sh` is consulted on a `Stop`;
   until the target is met it says "not done," and the Stop is refused. The
   mode staying active is the block.

2. **It spans iterations, not one turn.** The check reads the recorded metric
   *history* — prior iterations' artifacts — to know whether progress is real
   and the target is met. That history is the mode's own record across cycles
   (`context[eval-loop].record_file`), not another rule's live state.

3. **There is no file to guard.** The whole rule is activation + a
   goal-carrying context + an exit condition. No sub-ordinate file-guard, no
   per-file check — which is why a mode, not a file-guard, is the right shape.

## The parts

- **`mode/eval-loop/mode.yaml`** — `on: [{event: PreToolUse}]` (entry side;
  `enter` spots a declared loop). No exit event listed — a mode's `exit` is
  always checked on a Stop. No per-event `match` — a loop is recognised from
  the trajectory, not one event's fields.
- **`enter.sh`** — gets `ModeEnterPayload` (`{event, transcriptPath}`); reads
  the `#eval-loop` declaration (target + where the metric history is recorded)
  out of the trajectory, prints it as `context[eval-loop]`. This is unit 14's
  "adding a bunch of goals."
- **`exit.sh`** — the inverted verdict, on a `Stop`, given `ModeExitPayload`
  (`{event, transcriptPath, currentContext}`). Refuses the stop unless: the
  history file exists (RECORDED), its latest row carries a real value that
  isn't `estimated`/`model`-derived (HAPPENED + DETERMINISTIC), and the value
  meets the declared target. Only when all hold does the mode deactivate and
  let the turn end.

## What the stress test showed

The mode nature holds this without a fourth structure. The two things that
made #14 "not fit" before — the inverted verdict and the cross-iteration
history — both fall out of "a mode is active until its exit script says done":
the inversion is just that the blocked event is `Stop` instead of a write, and
the history is just the mode reading its own past. Nothing here needed a
concept the mode nature did not already have.
