---
title: Troubleshooting
description: A rule didn't fire, a refusal you didn't expect, or two rules colliding — how to diagnose each.
kind: guide
related:
  - authoring/prove-it-fires
  - concepts/refusal-contract
  - reference/resolution
---

Three things go wrong with a guardrail. Here's how to tell which, and what to do.

## My rule didn't fire

The action you expected to be refused went through. Work down this list:

1. **Did it load?** Run `sr session start < /dev/null`. If the declaration is
   invalid or bound to an event this build doesn't produce, it's named here — the
   rule never bound.
2. **Did the `match` actually match?** The coarse filter may be narrower than you
   think — a path prefix that doesn't match, a marker that isn't there, a context
   that wasn't active. Check the real event against the expression.
3. **Did the check run against the trajectory, or your assumption?** Confirm the
   firing the way [Prove it fires](/authoring/prove-it-fires) describes — see it
   refuse, don't infer it from the config.

Remember: loaded is not fired. A rule sitting there configured but never matching
is the exact failure to hunt for.

## An unexpected refusal

Something was refused that you thought was fine. Read the **reason** — every
refusal carries one (`{"reason": …}`, plain output, or a fallback). Then:

- If the reason names a **plugin** — `("some-rule" from plugin "sloprail")` — the
  rule came with a plugin, not your project. Decide whether it should apply, and
  [disable it](/authoring/disable-a-guardrail) if not.
- If a check **couldn't run** — a missing dependency, a judge timeout — that's a
  refusal by design ([fail-closed](/concepts/refusal-contract)). Fix what broke;
  don't work around it by loosening the rule.

## Rule shadowing / resolution

A project rule and a plugin rule share a name, and you're not sure which is
running. **The project wins**, and the shadowing is **reported** — the displaced
plugin rule is surfaced, not silent. If that's not what you want, rename yours or
disable theirs explicitly. See [resolution](/reference/resolution) for the
full precedence.
