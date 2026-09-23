---
title: grounding
description: A produced artifact or action carries a link to the specific source it derives from — and that link is verified true, not just present.
kind: explanation
sidebar:
  order: 4
related:
  - concepts/reconciliation
  - concepts/refusal-contract
  - reference/check-patterns
---

**Grounding** is the requirement that a produced artifact — or a performed action
— carries a reference to the *specific piece of a source* it derives from, and
that the reference is verified to be **true**, not merely present.

The failure it catches is fluent invention: an agent produces something plausible
that isn't actually tied to anything real. Grounding refuses the plausible-but-
unsourced by demanding the link and then checking it.

## Present is not enough — it must be true

A citation that points nowhere is worse than none, because it *looks* grounded.
So grounding has two levels: the reference must **exist** (the artifact names its
source), and, when it matters, the reference must **resolve** — the cited source
really says what the artifact claims it derives from.

## The source is not always the user

Grounding's source piece varies:

- A **deletion** grounded in the user's own words — the message that asked for it.
  A removal with no such link, or a link to words the user never said, is
  ungrounded and refused.
- A **research claim** grounded in another piece of the trajectory — not the user
  message, but the tool output it came from.
- An **action's proof** grounded in a tool result — a command's output asserted to
  be genuinely what it claims (a real screenshot the command produced, not a
  hallucinated one).

## The trajectory is the source

For claims about the run, the source being cited is the
[trajectory](/concepts/grounding) — the record of what actually happened. A
guardrail grounds a claim by resolving it against that record, never against what
the agent asserts. This is why grounding and the trajectory are the same idea
from two sides: the trajectory is the ground, grounding is the check that a claim
touches it.

## Grounding vs. completeness

Grounding verifies a link is *true*. [Reconciliation](/concepts/reconciliation)
and completeness verify a link *exists* — every item accounted for somewhere,
mechanically, without judging whether any one link is correct. Two different
questions; see the reconciliation page for the boundary.

For how grounding is expressed as a check, see [Guardrails](/reference/check-patterns).
