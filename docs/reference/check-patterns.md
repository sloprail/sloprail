---
title: Check patterns
description: The reusable shapes checks take — grounding, reconciliation, structure gating — and how they map to real config.
kind: explanation
related:
  - concepts/grounding
  - concepts/reconciliation
  - concepts/structure-gate
---

A few shapes recur across real guardrails. They aren't new natures — they're
*patterns* you build out of [checks](/reference/check-patterns). Each has a concept page
explaining the idea; here is how each lands as config.

## Grounding

A produced fact carries a link to its source, and the link is verified **true**.
As a check: read the artifact's cited source (a marker, a quote), resolve it
against the real source — the [trajectory](/concepts/grounding), a spec, a tool
output — and refuse if it doesn't hold. Often a cheap script (does a grounding
link exist at all?) gating a judge (does the artifact actually derive from it?).

See [grounding](/concepts/grounding).

## Reconciliation

A before and after must reduce to **empty** once exceptions and whitespace are
removed. As a check: compute the diff, strip the exception set (import rewrites,
whitespace) with regexes, and refuse on any non-empty residue. Pure script, no
model — the pass condition is empty output, which a script decides exactly.

See [reconciliation & completeness](/concepts/reconciliation).

## Completeness

Every member of a set is linked somewhere, checked mechanically. As a check:
enumerate the set, search for each item's link, and refuse if any is missing —
without judging whether the link is *correct* (that would be grounding). A script,
usually, walking the set and the tree.

See [reconciliation & completeness](/concepts/reconciliation).

## Structure gating

A write lands only inside a declared structure. This one is its own nature — a
[structure-gate](/concepts/structure-gate) with `allow`/`deny` entries — rather
than a check inside another rule, because the boundary is standing, not per-write.

See [structure-gate](/concepts/structure-gate).

## Precondition

An action is blocked until a required context (a skill, a doc) is in the
trajectory. Expressed as a [gate](/concepts/gate)'s `require`, often with no check
at all — the requirement is the whole rule.

See [precondition](/concepts/precondition).
