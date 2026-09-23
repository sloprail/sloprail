---
title: precondition
description: A required context (a skill, a doc) must be in the trajectory before an action.
kind: explanation
sidebar:
  label: Precondition
  order: 7
related:
  - guides/gate
  - use-cases/knowledge/acted-without-context
---

A **precondition** requires that some context — a skill loaded, a doc read — is already in the [trajectory](/concepts) before an action is allowed. It's answered against what actually happened, not what the agent claims.

A precondition is expressed as a [gate](/concepts/gate)'s `require`, often with no check at all.

→ [Authoring: the gate nature](/guides/gate)
