---
title: The refusal contract
description: How a check says no, and why a check that cannot run is itself a refusal.
kind: explanation
related:
  - guides
  - concepts/grounding
  - guides
---

Every guardrail exists to do one thing: **refuse an agent action**. How that
refusal is expressed, and what happens when a check can't even run, is the
contract the whole system rests on. It's worth understanding before you write
a rule, because the default behavior is deliberately unforgiving.

## Exit code is the verdict

A **script check**'s exit code is its answer. `0` permits. Any non-zero
refuses. That's the entire protocol — no framework, no return object.

A **judge check** is different in form but not in principle: it renders a
Jinja2 template and asks `sr-agent` for `{"pass": …, "reasoning": …}`.
`pass: false` is the refusal.

## Where the reason comes from

When a check refuses, sloprail needs a message to show the agent. It looks,
in order:

1. `{"reason": "..."}` JSON on stdout — the clean path.
2. Plain stdout.
3. stderr.
4. A generated fallback, if nothing else was said.

So the minimum viable refusal is: exit non-zero, print a reason. The agent
sees that reason and gets a chance to fix what it did.

## The load-bearing rule: a check that cannot run is a refusal

This is the part that surprises people. If a check **errors** — the script
isn't executable, a dependency is missing, the judge times out, an
expression fails to evaluate — sloprail does **not** shrug and let the
action through. It refuses.

This is *fail-closed*, and it's a choice, not an accident. The alternative
— failing open — means a guardrail silently stops protecting you the moment
something in it breaks, and you'd never know. A rule that can't run has
established nothing, and "established nothing" is not the same as "found
nothing wrong."

The discipline that follows: **a rule that loads but never fires is worse
than no rule**, because it gives you false confidence. Every guardrail you
write, you must *watch refuse* at least once.

## Why this matters for the product's whole claim

sloprail's promise is "enforced, not hoped." A prompt that says "don't do
X" is a hope — the model may or may not comply. A guardrail is enforcement
only if refusal is *guaranteed* when the condition holds, including when the
guardrail itself is having a bad day. Fail-closed is what makes the
guarantee real.

