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

`PreFileCreate` carries `resultKnown` for the same reason `PreFileUpdate` does,
and it can be **false** on a create: a `NotebookEdit` creating a fresh `.ipynb`
emits a `PreFileCreate` whose `newContent` is `""` and whose `resultKnown` is
false, because the tool's `new_source` is one cell, not the JSON document, so the
resulting bytes are **not derivable**. On that create `newContent == ""` does
**not** mean "genuinely empty" — it is the zero value an absent declared field
reads as, indistinguishable on the value alone from a real empty file. So the
unknowable case on a create is `resultKnown: false`, exactly as on an update — it
is not silence, and `newContent == ""` alone cannot tell "empty" from "unknown".
A Post kind carries settled bytes with no `resultKnown`, because there the write
has landed and nothing was predicted — but the engine still has to READ them,
and a file that is not a regular file once links are followed, or is larger
than one read takes, is reported with `newContent` `""` and `newContentKnown`
false. On a Post kind that is the unknown state.

**Instead:** decide, and write the decision in the body.

- On **either** Pre kind — create as well as update — guard on `resultKnown`
  before reading `newContent`. The correct shape is `resultKnown && <predicate
  over newContent>`; a script that reads `newContent` in a branch that *assumes*
  it is present is wrong on both kinds.
- If the rule needs content it cannot get (`resultKnown` false), **decide**. A
  gate whose job is to prevent **refuses** (fail closed): the engine does not do
  it for a gate, and a write nobody saw the bytes of has not been checked. A gate
  that only supplements a file-guard of the same name may **defer to the Post
  kind**: the tree diff after the cycle sees what actually landed, whatever
  produced it. A `Pre` rule that cannot predict should not guess.
- If the rule can still say something useful without content — a path rule, a
  naming rule — say it at `Pre` and let a Post rule cover the rest.
- On a **Post** kind, guard on `newContentKnown` before reading `newContent`,
  and fail closed when it is false: the rule could not see the settled file.
- Never write `newContent == ""` meaning "unknown". On a create `resultKnown`
  false is the unknown state; `newContent == ""` with `resultKnown` true is a
  real empty file, and the two are different.

**The trade, stated plainly:** silence at `Pre` (deferring) means the action is
not prevented, only reported afterwards. That is the honest answer for the
unknowable tier, and a rule's body should say which tier it relies on.

## How the check detects it

Two floors, both on the script's own text (comments are ignored):

- A script that handles a Post kind (names `PostFileCreate`, `PostFileUpdate`,
  `PostFileWrite` or `Post*`) and reads `newContent` without naming
  `newContentKnown` is flagged.
- A script that reads `newContent` without naming `resultKnown` is flagged,
  whatever the kind — a create-only hook is not exempt. A script that names
  `newContentKnown` and no Pre kind (a Post-only file-guard half) is exempt from
  this one.

A script that names the flag but still reads `newContent` in a branch that ignores
it is left to the judge (`judge-rules/pre-kinds-consult-resultknown`).
