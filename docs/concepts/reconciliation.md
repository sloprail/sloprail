---
title: reconciliation & completeness
description: Every item in a set is accounted for — checked mechanically, by a before/after that must reduce to nothing.
kind: explanation
sidebar:
  order: 5
related:
  - concepts/grounding
  - use-cases/coding/refactor-regenerated
  - concepts
---

**Completeness** is the requirement that every member of a set is linked, mapped,
or present *somewhere* — checked purely mechanically, without verifying that any
single link is correct. Absence of a link is itself the violation.

**Reconciliation** is completeness done as a before/after: a deterministic
comparison that must reduce to **empty** once a set of exception rules and
whitespace are removed. The pass condition is empty output — not a judgment.

These are one idea. Both ask "is anything unaccounted for?" and answer it by
mechanism, not by a model's opinion.

## The distinction from grounding

This is the boundary worth being precise about:

- **[Grounding](/concepts/grounding)** verifies a link is **true** — the artifact
  really derives from the source it cites.
- **Completeness / reconciliation** verifies a link **exists** — every item is
  accounted for *somewhere*, and whether any single link is correct is not this
  check's business.

You can be complete and ungrounded (every item linked, some links false) or
grounded but incomplete (the links you made are true, but you missed items). They
catch different failures, so they are different primitives.

## How reconciliation runs

The mechanism is a diff that must cancel to nothing:

1. Compare the old locations against the new.
2. Apply exception rules (as regexes) — import path rewrites, for instance — and
   strip whitespace.
3. What's left is the *residue*. Empty residue means everything moved intact;
   non-empty residue is content that changed, vanished, or was regenerated.

The refactor guardrail is exactly this: a moved block must reconcile
byte-for-byte against its origin, minus imports and whitespace. Non-empty residue
is a rewrite wearing the shape of a move, and it's refused.

## Where each shows up

- **Reconciliation** — a refactor's moves must all land byte-identical.
- **Completeness** — every intake message processed and linked somewhere; every
  entity in a set interlinked; no orphan left unmapped.

For how these are expressed as checks, see [Guardrails](/concepts).
