---
title: structure-gate
description: A deny-by-default map of the tree — a write lands only if its path is on the allowed list.
kind: explanation
sidebar:
  order: 6
related:
  - guides/gate
  - concepts/marker
  - guides/file-guard
---

A **structure-gate** is a list of paths where writing *is* allowed; everything
outside it is disallowed. Deny-by-default over the file tree.

It is the concrete, standing boundary: not "check this write" at a moment, but "no
write lands anywhere except inside this structure," always.

## Structure-gate vs. gate

Despite the name, this is distinct from a plain [gate](/guides/gate). A gate is
a momentary, one-shot checkpoint on an event — abstract, general. A structure-gate
is the specific, standing rule over the tree: the set of allowed write locations.
One is a checkpoint you pass; the other is a map that's always in force.

## The useful side effect

When every place is disallowed except a declared structure, an agent that can't
find a valid place to write is pushed to **clarify or create the structure
first** — to figure out where a thing belongs before dumping it somewhere. That's
not a second primitive; it's a downstream effect of denying by default. The gate
refuses the stray write, and the agent's next move is to establish the right home.

## Structure-gate vs. marker

A structure-gate is **path-based** — it allows or denies by where a file is. Its
[marker](/concepts/marker)-based analogue allows by a marker the artifact
carries rather than by path. Related in spirit — both are allow-lists — but one
keys on location and the other on a label, so they're separate primitives.

## Reach for it when

You want a tree with a fixed shape — memories, tasks, a knowledge base — where
writing outside the sanctioned layout is a mistake, and the agent should be made
to place things deliberately.

For the exact configuration, see [Nature YAML shapes](/guides/file-guard).
