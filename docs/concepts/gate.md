---
title: gate
description: A checkpoint on an event that lets an action through or blocks it — the only primitive that can stop a turn.
kind: explanation
sidebar:
  order: 2
related:
  - concepts/file-guard
  - concepts/context
  - concepts/precondition
  - reference/nature-shapes
---

A **gate** is the simplest nature: a checkpoint that wakes on an event, evaluates
its preconditions and checks, and either lets the action through or **blocks** it.
It is one-shot — it decides at the moment and is done, unlike a
[file-guard](/concepts/file-guard) that keeps re-checking.

A gate is the **only** primitive that can stop a turn. If you need something
blocked, a gate does it.

## The value over a bare hook

You could write this as a hook. The reason not to: a real gate is mostly
*trajectory plumbing* — reading the record of what happened, in a form every hook
author would otherwise rewrite by hand. A ported example of "was the required
skill loaded" was ~137 lines, of which ~20 were the actual rule and ~115 were
that plumbing. The gate primitive is the plumbing, factored out: you write the
rule, it handles reading the run.

## `require` — preconditions as the whole rule

A gate often has no check at all. When the rule is purely "this must have
happened first," a `require` says it: a [precondition](/concepts/precondition)
that a skill loaded or a [context](/concepts/context) is active. `require` also
**orders** — a gate that reads a context's state names it in `require`, and the
context is settled before the gate runs.

## Reach for it when

The thing you're checking is about the **turn** — did a promised action land, was
a required skill loaded, is a declared scope complete. Block at the checkpoint;
don't hope it happened.

For the exact YAML shape, see [Nature YAML shapes](/reference/nature-shapes).
