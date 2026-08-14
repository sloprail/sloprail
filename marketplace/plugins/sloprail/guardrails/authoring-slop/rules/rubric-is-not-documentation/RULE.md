---
enforced: false
---

# The rubric is a prompt; the body is documentation

**Mistake:** a judge hook reading its whole `GUARDRAIL.md` body as the prompt.

The body argues the rule to a reader — why a judge and not a matcher, why per
file, why it fails open. None of that is a standard a model applies, and all of
it was being sent and paid for on every judged file.

**Three costs, measured on a real rule:**

- every paragraph of reasoning sent on every judged file, and billed
- editing an explanation could silently move a verdict
- a diff could not tell you whether a change altered the rule or described it

**Instead:** keep the standard in its own file beside the declaration. A wrong
rubric is a wrong verdict; a wrong doc is a confused reader; neither should be
able to become the other by accident.

## Why this is not enforced mechanically

Detecting that a script reads its own body as a prompt is possible, but the
shapes vary enough that a check would be guessing. Documented, left to review.
