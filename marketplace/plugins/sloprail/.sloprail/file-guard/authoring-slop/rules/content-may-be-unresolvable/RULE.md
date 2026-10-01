---
enforced: true
---

# Have a strategy for content that could not be resolved

**Mistake:** treating an empty `newContent` as "the file is empty".

A `Pre` event is a **prediction**, and not every action's result is knowable
before it happens. `echo hi > f.md` is derivable; `some-unknown-tool > f.md` is
not. So a rule reading content has three cases, not two: known, known-empty, and
unknowable.

**What the engine does today, measured** (from `internal/filemod/module.go`'s
`Kinds()` and `services/sr-session/nature_fileguard.go`'s
`isUnderivablePreWrite`):

| kind | carries | when the result cannot be derived |
| --- | --- | --- |
| `PreFileCreate` | `newContent` + **`resultKnown`** | `resultKnown` is false — ask it |
| `PreFileUpdate` | `newContent` + **`resultKnown`** | `resultKnown` is false — ask it |
| `PostFileCreate` / `PostFileUpdate` | `newContent` (settled) + **`newContentKnown`** | `newContentKnown` is false — the settled file could not be READ (a link to a FIFO or a device, or past the read cap) |
| `Changeset` (a file-guard) | `changeset.files[].newContent` — a committed blob | never: committed content is always known, and there is no flag to consult |

An absent declared field reads as its zero value, so `newContent == ""` is
indistinguishable on the value alone from a real empty file: a `NotebookEdit`
creating a fresh `.ipynb` emits a `PreFileCreate` with `newContent` `""` and
`resultKnown` false.

**Instead:** decide, and write the decision in the body.

- On **either** Pre kind — create as well as update — guard on `resultKnown`
  before reading `newContent`: `resultKnown && <predicate over newContent>`.
- If the rule needs content it cannot get (`resultKnown` false), **decide**. A gate
  whose job is to prevent **refuses** (fail closed); the engine does not do it for
  a gate. A gate that only supplements a file-guard of the same name may **defer to
  the Post kind**, whose tree diff sees what actually landed.
- If the rule can still say something useful without content — a path rule, a
  naming rule — say it at `Pre` and let a Post rule cover the rest.
- On a **Post** kind, guard on `newContentKnown` before reading `newContent`, and
  fail closed when it is false.

**The trade, stated plainly:** silence at `Pre` (deferring) means the action is
not prevented, only reported afterwards. That is the honest answer for the
unknowable tier, and a rule's body should say which tier it relies on.

## How the check detects it

Two floors, both on the script's own text (comments are ignored):

- A script that handles a Post kind (names `PostFileCreate`, `PostFileUpdate`,
  `PostFileWrite` or `Post*`) and reads `newContent` without naming
  `newContentKnown` is flagged.
- A script that reads `newContent` without naming `resultKnown` is flagged,
  whatever the kind — a create-only hook is not exempt. A script that names no
  Pre kind and either names `newContentKnown` (a Post-only script) or reads
  `.changeset` (a file-guard, whose committed content is always known) is exempt
  from this one.

A script that names the flag but still reads `newContent` in a branch that ignores
it is left to the judge (`judge-rules/pre-kinds-consult-resultknown`).
