---
title: precondition
description: Before an action is allowed, some required context must already be in the trajectory — a skill loaded, a doc read.
kind: explanation
sidebar:
  order: 7
related:
  - guides/gate
  - concepts/grounding
  - guides/file-guard
---

A **precondition** requires that, before an action is allowed, some context has
already entered the [trajectory](/concepts/grounding): a skill was loaded, a
document was read, an agent was dispatched a certain way. It's enforced by denying
at the pre-phase — the action doesn't land until the requirement is met.

The failure it catches is acting without the context that action needed. An agent
edits a file that a skill was supposed to shape first; a record gets written
before the doc that governs its format was read. The precondition refuses the
action until the prerequisite is actually present.

## It reads what happened, not what was claimed

A precondition is answered against the trajectory — *was this skill actually
loaded this session* — not against the agent's assertion that it was. "Available"
is not "loaded"; the precondition checks the real event, not the intent.

## Where it lives

A precondition is expressed as a [gate](/guides/gate)'s `require`. When the rule
is purely "this must have happened first," the `require` is the whole gate — no
check needed:

- Writing under a governed path is blocked until the skill that governs it loaded.
- An action that depends on a dispatched sub-agent is blocked until that dispatch
  is in the record.

## A deliberately narrow abstraction

A precondition is a *high-level* question — "was X loaded" — not a raw query over
the trajectory. That's on purpose: a raw trajectory-query is too low-level to
expose directly, so preconditions are narrow, named abstractions, each added as a
new need appears. You ask the specific question, not build it from primitives.

For the exact `require` syntax, see [Nature YAML shapes](/guides/file-guard).
