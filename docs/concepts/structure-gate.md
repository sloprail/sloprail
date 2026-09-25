---
title: Structure-gate
description: A deny-by-default allowlist over the file tree, composed from the project's own piece and each plugin's scoped piece.
kind: explanation
sidebar:
  order: 6
related:
  - guides/structure-gate
  - use-cases/long-runs/run-drifts-structure
  - concepts/marker
---

A **structure-gate** is a list of paths where writing is allowed; everything it
covers is denied by default. Unlike the other primitives it isn't a per-rule
check but a standing boundary configured directly, one `structure.yaml` per
`.sloprail` root — a project's own, and a `scope:` entry each installed plugin
may ship for the slice of the tree it owns. Every one in force **composes**:
a write is allowed once ANY covering file's `allow` matches it and NO covering
file's `deny` does, and a path no file covers has no opinion from the gate at
all — a plugin does not need the project to also list its shapes.

→ [Configuring the structure gate](/guides/structure-gate)
