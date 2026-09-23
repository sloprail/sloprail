---
title: Prove it fires
description: Loading is not firing. Make the violation happen and watch the rule refuse — the step that turns a hope into enforcement.
kind: guide
related:
  - concepts/refusal-contract
  - authoring/write-a-guardrail
  - authoring/troubleshooting
---

This is the step most people skip, and it's the whole point. A guardrail that
*loaded* has done nothing yet. Only a guardrail you have watched *refuse* is
actually protecting you.

## Why loading is not firing

"Loaded" means the declaration parsed and bound to an event. "Fired" means the
check actually ran and refused when it should. They're easy to confuse, and
confusing them gives you false confidence — a rule sitting there configured,
protecting nothing, that you *believe* is protecting you. That belief is worse
than knowing you have no rule.

## The test

Two runs, both against the real agent:

1. **Make the violation happen.** Ask the agent to do exactly the thing the rule
   should refuse. The action must be **refused**, and the reason you wrote must be
   shown back to the agent.
2. **Do the allowed thing.** Ask for the near-neighbour that should pass. It must
   land.

If both happened, the rule is real. If the bad action went through, your rule
loaded but didn't fire — which is the failure this whole product exists to
prevent.

## Confirm against the trajectory, not the config

Check that the refusal actually happened in the run — the
[trajectory](/concepts/grounding) is what tells firing apart from loading. Don't
infer "it must have fired" from the config being present; see it refuse.

## Make it a habit

Every guardrail you write, watch it refuse at least once before you trust it. This
is the discipline the [refusal contract](/concepts/refusal-contract) is built to
support: enforcement is only real once you've seen the "no."
