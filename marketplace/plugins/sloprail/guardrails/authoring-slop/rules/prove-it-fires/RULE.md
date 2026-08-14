---
enforced: false
---

# Prove it fires; loading is not firing

**Mistake:** reporting a rule as working because it loaded, or because a
hand-made payload reached the script.

**Measured failures this rule exists for:**

- A guardrail whose script was not executable refused *everything* — the engine
  says "will refuse until this is fixed", which is correct and easy to miss.
- A hook whose `guardrailDir` was absent from a hand-made probe payload silently
  failed open, through three probes that all looked successful.
- A plugin declared in `settings.json` but never installed ran **zero times** for
  a whole day. `extraKnownMarketplaces` declares a marketplace; it does not
  install anything. Hooks load from the plugin cache.

**Instead:** cause the guarded action for real and see the refusal, then cause
the permitted action and see it pass. Both directions.

If a rule replaces an existing check, run both on the same input and account for
every disagreement before removing the old one. A migration whose first act is to
disagree with its own oracle has nothing left to check against.

## Why this is not enforced mechanically

It is a claim about how the author verified, not about the script's text.
