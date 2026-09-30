# deterministic-refactoring (context + gates + file-guard)

**Natures:** context + two gates + file-guard

A refactor must be mechanical, not regenerated: the agent declares the scope
of moves upfront, and every declared move must land and reconcile
byte-identically against its origin before the turn can end.

## Install

Make sure sloprail is installed — see the docs
[install page](https://sloprail.com/docs/getting-started/install/).

Then copy this example into your project:

```bash
git clone --depth 1 https://github.com/sloprail/sloprail /tmp/sloprail-clone && mkdir -p .sloprail && cp -R /tmp/sloprail-clone/examples/deterministic-refactoring-mode/.sloprail/. .sloprail/ && rm -rf /tmp/sloprail-clone
```

Then declare a refactor, leave a move unfinished, and confirm you see the
refusal below.

## The rule

A refactor — splitting one file into several, moving a function between files
— must be MECHANICAL, not regenerated. The agent first declares intent
(`#refactor`) and the SCOPE as a set of moves fixed upfront ("don't know yet in
which files, but they're already set"); then, after the moves, every declared
move must have LANDED **and** the moved content must reconcile byte-identically
(minus imports and whitespace) against its origin.

Two independent failures, two natures:

- **A move that regenerated instead of carrying the bytes** — refused BEFORE the
  write lands by the `moved-content-reconciles` gate (on `PreFileCreate` /
  `PreFileUpdate`); the same-named file-guard re-runs the reconcile at `Stop` on
  the settled file.
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

## Why the reconcile is a gate AND a file-guard

A move that regenerated is a loss to refuse before it happens, and only a gate
sees a write before it lands: a file-guard acts only at `Stop`, on the settled
file. So the reconcile is split:

- **`gate/moved-content-reconciles`** is the prevention. It reads the pending
  bytes and refuses a non-reconciling move, so the write never lands. It also
  refuses a write whose result the engine cannot compute (`sed -i` on a file that
  already carries a `moved-from` marker): a gate does not fail closed on that by
  itself, and reconciling against bytes nobody saw is no check.
- **`file-guard/moved-content-reconciles`** is the after-check, the same
  reconcile on the settled file at `Stop`. It is the backstop for a write the gate
  could not see. One library per script, in the file-guard folder (`<script>-lib.sh`), holds the shared logic; each half keeps a thin entry that reads its own event kind (the gate's `Pre*`, the file-guard's `Post*`) and sources it.

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
  marker's kind stays `moved-from`, so the gate's and the file-guard's `moved-from` marker match and the reconcile
  script's `.kind == "moved-from"` selection are untouched. The declaration and the landed marker share one vocabulary.
- *embed a nickname in the marker kind* (`sr:moved-from:beta …`) — rejected: that
  changes the marker's kind to `moved-from:beta`, which would break every reader
  that matches `moved-from`, rippling through the file-guard and reconcile script.
- *a count-only check* ("N declared → ≥N markers") — rejected as too weak: it
  cannot tie a specific declared move to a specific landed one.

## The parts

- **`context/refactoring/context.yaml`** — `on: [{event: PreToolUse}, {event:
  PostTagWrite}]`. Two triggers because the context is read at two moments:
  - **PreToolUse** activates the scope BEFORE a marked write, which is what the
    reconcile gate's `match: context["refactoring"].active` reads at that write. (At
    this moment the current assistant turn is not yet in the transcript, so `enter`
    cannot read the scope here — it just opens the scope.)
  - **PostTagWrite** fires at `Stop`, once the turn IS settled, so `enter` can read
    the `#refactor scope=...` declaration and populate `declared_markers` BEFORE
    the gate reads it (enters run before gates in the Stop cycle).
- **`context/refactoring/enter.sh`** — gets `ContextEnterPayload`; reads the
  `#refactor` declaration out of the trajectory (`sr-session trajectory
  normalize --events PostTagWrite`, tag at `.events[].tags[].label`),
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
- **`gate/moved-content-reconciles/`** — the pre-write byte check, active only
  while the context is. Reconciles the pending bytes of a moved file against its
  pinned origin, dropping imports and whitespace (the exception rules), and refuses
  before the write lands.
- **`file-guard/moved-content-reconciles/`** — the same check at `Stop`, on the
  settled file.

## The refusal contract

Every refusal here is a clean `{"reason": "…"}` object on stdout — the engine reads
`reason` and turns it into the block text. (The old wrapped shape still
worked because the engine reads `reason` regardless, but the scripts here use the
current shape.)
