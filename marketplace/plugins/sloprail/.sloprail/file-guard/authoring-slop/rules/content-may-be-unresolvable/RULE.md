---
enforced: true
---

# Have a strategy for content that could not be resolved

**Mistake:** treating an empty `newContent` as "the file is empty".

A `Pre` event is a **prediction**, and not every action's result is knowable
before it happens. `echo hi > f.md` is derivable; `some-unknown-tool > f.md` is
not. So a rule reading content has three cases, not two: known, known-empty, and
unknowable.

**What the engine does today, measured:**

| kind | field | when the result cannot be derived |
| --- | --- | --- |
| `PreFileUpdate` | `newContent` (optional) + **`resultKnown`** | `resultKnown` is false — ask it |
| `PreFileCreate` | `newContent` (required, always present) | **no event is emitted at all** |

So on a create, `newContent == ""` means genuinely empty — `touch f.md` produces
it. The unknowable case is silence, not an empty string. That asymmetry is
deliberate: an absent declared field reads as its zero value, so a create
carrying `newContent: ""` for an unknown result would be indistinguishable from a
real empty file, and `newContent == ""` is exactly the rule an author writes to
catch that.

**Instead:** decide, and write the decision in the body.

- If the rule needs content it cannot get, **defer to the Post kind**. The tree
  diff after the cycle sees what actually landed, whatever produced it. A `Pre`
  rule that cannot predict should not guess.
- If the rule can still say something useful without content — a path rule, a
  naming rule — say it at `Pre` and let a Post rule cover the rest.
- Never write `newContent == ""` meaning "unknown". It is a real state.

**The trade, stated plainly:** silence at `Pre` means the action is not
prevented, only reported afterwards. That is the honest answer for the
unknowable tier, and a rule's body should say which tier it relies on.

## How the check detects it

Reading `newContent` (in the new format, `.event.newContent`; the old envelope
spelled it `.event.fields.newContent`) without mentioning `resultKnown` anywhere.
On a `PreFileUpdate` an absent `newContent` reads as `""`, which is
indistinguishable from a write that empties the file. (On `PreFileCreate`
`newContent` is always present, so a rule bound only to creates needs no
`resultKnown`.)

The `PreFileCreate` half is **not mechanically checkable** — "does this rule have
a strategy" is a question about the body, not the script. It is documented here
and left to review.
