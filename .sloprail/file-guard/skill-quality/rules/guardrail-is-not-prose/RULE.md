---
enforced: true
---

# A skill shows correct usage; it does not enumerate what the guardrail forbids

**Mistake:** a prohibitions section — "never do X", "Y is not allowed", a table
of banned patterns — restating in prose what a guardrail already refuses at the
moment it matters.

The prose is a second copy of the rule, so it goes stale the first time the
guardrail is tightened — and the reader who follows the skill then writes what
the engine refuses.

**Instead:** show what correct looks like. Examples may contain real content and
should — a reader copies the shape from a worked example, and gets it right
without ever being told what the wrong shape was.

Naming a guardrail so the reader knows what will judge their work is fine, as is
one line saying enforcement exists and where to read it. What is not fine is the
prose taking over the enforcing: the prohibition spelled out, the banned list
enumerated, the refusal conditions restated.
