# deterministic-refactoring (mode)

**Unit:** [12_deterministic-refactoring](/Users/nsviridenko/ws/sloprail/strategy/memories/topics/20260812_no-slop/units/12_deterministic-refactoring/UNIT.md)
**Nature:** mode ([decision 20260818_no-slop-primitives](/Users/nsviridenko/ws/sloprail/strategy/memories/decisions/20260818_no-slop-primitives/DECISION.md), slice 4, candidate list)

## The rule

A refactor — splitting one file into several, moving a function between files
— must be MECHANICAL, not regenerated. The agent first declares intent
(`#refactor`) and the SCOPE as a marker set fixed upfront ("don't know yet in
which files, but they're already set"); then, after the moves, every declared
marker must be present AND the moved content must reconcile byte-identically
(minus imports and whitespace) against its origin.

## Why mode, not file-guard

This is the first example of the **mode** nature, and it shows the two things
a file-guard alone cannot do:

1. **A lifecycle across the turn, not one file's state.** "Declared a refactor
   but never finished it" is a fact about the *turn*, not about any single
   file. The mode is entered when the declaration appears and stays active
   until every declared marker has landed — refusing a `Stop` in between. No
   per-file guard can see "the set is incomplete."

2. **A context that switches a file-guard on.** The `moved-content-reconciles`
   file-guard is relevant ONLY inside a declared refactor — a stray
   `sr:moved-from` marker outside the mode is not its business. That's the
   **guard → mode link**: the guard's own `match` is
   `refactoring and marker.kind == "moved-from"`, naming the mode as a
   variable rather than the mode listing the guard.

## The parts

- **`mode/refactoring/mode.yaml`** — `on: [{event: PreToolUse}]` (the entry
  side only; `enter` sees a declaration before a write). No exit event is
  listed: a mode's `exit` is always checked on a Stop. No per-event `match` —
  recognising a refactor needs the trajectory, not one event's fields.
- **`enter.sh`** — gets `ModeEnterPayload` (`{event, transcriptPath}`); reads
  the `#refactor` declaration out of the trajectory (not a CLI call the agent
  had to make), extracts the declared marker set, and prints it as
  `context[refactoring]`. Silence = not a refactor, don't activate.
- **`exit.sh`** — on a `Stop`, gets `ModeExitPayload` (`{event, transcriptPath,
  currentContext}` — `currentContext` being this mode's own extracted
  context); checks every declared marker actually landed; refuses the stop if
  any is missing. Done = mode deactivates.
- **`file-guard/moved-content-reconciles/`** — the per-file byte check, active
  only while the mode is. Reconciles a moved file against its pinned origin,
  dropping imports and whitespace (unit 12's exception rules).

## Where the deciding lives: `enter`, not the per-event `match`

Each `on` trigger may carry an optional `match` expression to narrow on the
event alone — useful for e.g. a `PreCommandInvoke` where the parsed command is
right there. This mode uses none: the real question (is this a refactor?
what's the scope?) needs the trajectory, so both triggers wake unconditionally
and `enter` carries the whole decision. A mode keyed on a specific command
would push some of that into `match` instead — the split is per case.
