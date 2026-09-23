---
title: tag
description: A durable label placed in the trajectory — the agent's own message — that anchors a rule to a point in the stream of actions.
kind: explanation
sidebar:
  order: 9
related:
  - concepts/marker
  - concepts/context
  - concepts/grounding
---

A **tag** is a label placed **in the trajectory**, in the agent's own message: a
declaration like `#refactor`. It anchors a rule to a point in the *stream of
actions* — a moment when the agent declared an intent or a scope.

A tag sits in the trajectory, which is **append-only and time-ordered**. The
question a tag answers is "did this tag appear in the stream?" — a fundamentally
different question from a [marker](/concepts/marker)'s "is this present in the tree
now?" That difference is why they are two primitives, not one with a host setting:
one is for the static half of the world, one for the temporal half.

## What a tag anchors

- A **scope declaration** — `#refactor scope=…` opens a
  [context](/concepts/context); the context's `enter` reads the tag out of the
  trajectory and records what the agent committed to.
- An **intent** the rest of a rule keys on — "the agent said it was doing X," read
  from the record rather than asserted.

A tag is read against the [trajectory](/concepts/grounding) — it counts only if it
actually appeared in the run, at a moment that has settled into the transcript.

## Tag vs. marker

A tag's counterpart is the [marker](/concepts/marker) — same idea (a durable
label), opposite host. A tag declares something *in the run*; a marker pins
something *on an artifact*.
