---
title: Context
description: An activatable scope the agent is inside — it tracks, and never blocks.
kind: explanation
sidebar:
  order: 3
related:
  - guides/context
  - use-cases/coding/refactor-regenerated
  - guides/state-management
---

A **context** is a [nature](/concepts): a scope the agent is *inside*, which tracks state while active. A context never blocks — it pairs with a [gate](/concepts/gate) that does.

Reach for it when a rule must remember something across a turn to check later.

→ [Writing a context](/guides/context)
