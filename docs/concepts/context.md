---
title: context
description: An activatable scope the agent is inside — it enforces things while active, and never blocks.
kind: explanation
sidebar:
  order: 3
related:
  - concepts/gate
  - concepts/tag
  - reference/nature-shapes
  - guides/coding/refactor-regenerated
---

A **context** is a scope the agent can be *inside*. It activates at some point,
enforces certain things while active, and exits under its own condition — or
never, if it should stay on once triggered. Where a [file-guard](/concepts/file-guard)
hangs on a file's state and a [gate](/concepts/gate) fires on one event, a context
is a *mode you are in*.

Think of it as: "now a refactor is happening" — and while that's true, other
guardrails behave differently.

## A context never blocks

This is the load-bearing rule, and the most common mistake. A context **tracks**;
it does not refuse. Its lifecycle hooks decide only whether the context stays
open — they cannot stop a turn. If a context needs something enforced, it pairs
with a [gate](/concepts/gate) that does the blocking.

The refactor example is exactly this pairing: a context tracks the declared
scope and stays active until the moves land; a separate gate reads that scope and
refuses the turn if one is missing. The context remembers; the gate blocks.

## How it activates

A context is activated the way a scope gets declared — often a
[tag](/concepts/marker) the agent drops in its own message (`#refactor`),
read out of the [trajectory](/concepts/grounding#the-trajectory-is-the-source).
Its `enter` records what it needs; its `exit` decides whether the scope is
finished.

## Reach for it when

A rule needs to **remember something now to check later** — a declared scope, an
accumulating set of actions — across a turn or several. Track it in a context,
block with a gate.

For the exact YAML shape, see [Nature YAML shapes](/reference/nature-shapes).
