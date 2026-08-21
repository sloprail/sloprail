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
| `PostFileCreate` / `PostFileUpdate` | `newContent` (settled, no `resultKnown`) | cannot happen — the bytes have landed |

`PreFileCreate` carries `resultKnown` for the same reason `PreFileUpdate` does,
and it can be **false** on a create: a `NotebookEdit` creating a fresh `.ipynb`
emits a `PreFileCreate` whose `newContent` is `""` and whose `resultKnown` is
false, because the tool's `new_source` is one cell, not the JSON document, so the
resulting bytes are **not derivable**. On that create `newContent == ""` does
**not** mean "genuinely empty" — it is the zero value an absent declared field
reads as, indistinguishable on the value alone from a real empty file. So the
unknowable case on a create is `resultKnown: false`, exactly as on an update — it
is not silence, and `newContent == ""` alone cannot tell "empty" from "unknown".
Only a Post kind carries settled bytes with no `resultKnown`, because there the
write has landed and nothing was predicted.

**Instead:** decide, and write the decision in the body.

- On **either** Pre kind — create as well as update — guard on `resultKnown`
  before reading `newContent`. The correct shape is `resultKnown && <predicate
  over newContent>`; a script that reads `newContent` in a branch that *assumes*
  it is present is wrong on both kinds.
- If the rule needs content it cannot get (`resultKnown` false), **defer to the
  Post kind**. The tree diff after the cycle sees what actually landed, whatever
  produced it. A `Pre` rule that cannot predict should not guess.
- If the rule can still say something useful without content — a path rule, a
  naming rule — say it at `Pre` and let a Post rule cover the rest.
- Never write `newContent == ""` meaning "unknown". On a create `resultKnown`
  false is the unknown state; `newContent == ""` with `resultKnown` true is a
  real empty file, and the two are different.

**The trade, stated plainly:** silence at `Pre` means the action is not
prevented, only reported afterwards. That is the honest answer for the
unknowable tier, and a rule's body should say which tier it relies on.

## How the check detects it

Reading `newContent` (in the new format, `.event.newContent`; the old envelope
spelled it `.event.fields.newContent`) without mentioning `resultKnown` anywhere.
On **either** Pre kind an absent `newContent` reads as `""`, which is
indistinguishable from a write that empties the file — so the grep fires
regardless of kind, and a create-only hook is **not** exempt. The old claim that
"on `PreFileCreate` `newContent` is always present, so a create needs no
`resultKnown`" was wrong: a `NotebookEdit` fresh-`.ipynb` create carries
`resultKnown: false` and a non-derivable `newContent`, and a create-only hook
reading `newContent` without consulting `resultKnown` is exactly the bug this
catches.

The grep catches the *missing* `resultKnown` — the field named nowhere in the
script. What it cannot catch is a script that *does* name `resultKnown` yet still
reads `newContent` in a branch that assumes the create case is derivable (the
grep sees the word `resultKnown` and stays silent). That subtler shape — the
derivable-create assumption — is left to the **judge** check (its
`rules/pre-kinds-consult-resultknown` reasons about *when* `newContent` may be
read per kind, not merely *whether* `resultKnown` appears). The two are
complementary: the grep is the cheap floor, the judge the reasoning above it.
