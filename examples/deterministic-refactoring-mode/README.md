# deterministic-refactoring (context + Stop gate)

**Natures:** context + gate + file-guard

## The rule

A refactor — splitting one file into several, moving a function between files
— must be MECHANICAL, not regenerated. The agent first declares intent
(`#refactor`) and the SCOPE as a set of moves fixed upfront ("don't know yet in
which files, but they're already set"); then, after the moves, every declared
move must have LANDED **and** the moved content must reconcile byte-identically
(minus imports and whitespace) against its origin.

Two independent failures, two natures:

- **A move that regenerated instead of carrying the bytes** — caught PREVENTIVELY
  by the `moved-content-reconciles` file-guard, before the write lands.
- **A declared move that never happened at all** — caught at `Stop` by the
  `refactor-complete` GATE, which refuses the turn.

## Why the completeness check is a GATE, not the context's exit

Earlier this example put the "did every declared move land?" check in the
context's `exit`. That was **dead code**: in the nature format a context's `exit`
is PURE LIFECYCLE — the engine reads its verdict only to flip the context's own
`active` flag, and it CANNOT block a `Stop`
(`services/sr-session/nature_context.go`). Only a **gate** blocks a turn.

So the split is:

- The **context** (`refactoring`) TRACKS the declaration — it records the declared
  moves into its payload and stays active while any is outstanding. It never
  blocks.
- The **Stop gate** (`refactor-complete`) READS that payload and BLOCKS the turn
  when a declared move is missing. It runs BEFORE the context's exit in the Stop
  cycle, and the context's exit then reads the gate's verdict to decide whether to
  close (this is the same context+gate pairing `research-rigor` and
  `completeness-artifact-on-trigger` use).

## The declared-scope ↔ landed-marker correspondence (the design choice)

For the completeness check to work, a declared move has to be recognisable once it
lands. A move WRITES a marker `// sr:moved-from <path>@<sha>:<start>-<end>` — the
`fqn` after `sr:moved-from` is what pins the origin. The declaration therefore
names those **same fqns**:

```
#refactor scope=src/beta.go@<sha>:10-24,src/gamma.go@<sha>:3-9
```

Each `scope=` token IS the fqn a completed move's marker carries. That literal
correspondence is what lets the gate answer "did this move land?" by a plain
search of the tree for a file carrying `sr:moved-from <that fqn>` — no
logical-nickname-to-marker mapping to guess.

This was a deliberate choice among three:

- **(chosen) declare the actual fqns.** Zero change to the marker convention: the
  marker's kind stays `moved-from`, so the file-guard's `any(markers, .kind ==
  "moved-from")` and the reconcile script's `.kind == "moved-from"` selection are
  untouched. The declaration and the landed marker share one vocabulary.
- *embed a nickname in the marker kind* (`sr:moved-from:beta …`) — rejected: that
  changes the marker's kind to `moved-from:beta`, which would break every reader
  that matches `moved-from`, rippling through the file-guard and reconcile script.
- *a count-only check* ("N declared → ≥N markers") — rejected as too weak: it
  cannot tie a specific declared move to a specific landed one.

## The parts

- **`context/refactoring/context.yaml`** — `on: [{event: PreToolUse}, {event:
  PostTagWrite}]`. Two triggers because the context is read at two moments:
  - **PreToolUse** activates the scope BEFORE a marked write, which is what the
    file-guard's `match: context["refactoring"].active` reads at that write. (At
    this moment the current assistant turn is not yet in the transcript, so `enter`
    cannot read the scope here — it just opens the scope.)
  - **PostTagWrite** fires at `Stop`, once the turn IS settled, so `enter` can read
    the `#refactor scope=...` declaration and populate `declared_markers` BEFORE
    the gate reads it (enters run before gates in the Stop cycle).
- **`context/refactoring/enter.sh`** — gets `ContextEnterPayload`; reads the
  `#refactor` declaration out of the trajectory (`sr-session trajectory
  normalize --events PostTagWrite`, tag at `.events[].fields.tags[].label`),
  extracts the declared fqns, and prints them as
  `context[refactoring].payload.declared_markers`.
- **`context/refactoring/exit.sh`** — PURE LIFECYCLE. On a `Stop` it reads the
  `refactor-complete` gate's settled verdict from `gates`: `pass` → deactivate
  (the refactor is done); otherwise stay active so the next cycle's `Stop`
  re-runs the gate. This is what carries a MULTI-CYCLE refactor. It never refuses.
- **`gate/refactor-complete/gate.yaml`** — `on: [{event: Stop, match:
  context["refactoring"].active}]`, `require: [{context: refactoring}]`. Wakes at
  `Stop` only when a refactor is active; `require` orders the context first so the
  gate reads its settled `declared_markers`.
- **`gate/refactor-complete/verify-declared-moves-landed.sh`** — reads
  `declared_markers` off its stdin (`.context.refactoring.payload.declared_markers`)
  and, for each declared fqn, searches the workspace for a file carrying
  `sr:moved-from <fqn>`. Any missing → refuse the turn with a `{"reason": …}`
  object on stdout. All present → permit.
- **`file-guard/moved-content-reconciles/`** — the per-file byte check, active only
  while the context is. Reconciles a moved file against its pinned origin, dropping
  imports and whitespace (the exception rules).

## The refusal contract

Every refusal here is a clean `{"reason": "…"}` object on stdout — the engine reads
`reason` and turns it into the block text. (The old wrapped shape still
worked because the engine reads `reason` regardless, but the scripts here use the
current shape.)
