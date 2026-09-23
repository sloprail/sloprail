---
title: Structure-gate
description: A deny-by-default allowlist over the file tree — the one thing a project configures directly.
kind: explanation
sidebar:
  order: 6
related:
  - guides/structure-gate
  - use-cases/long-runs/run-drifts-structure
  - concepts/marker
---

A **structure-gate** is a list of paths where writing is allowed; everything
outside is denied — deny-by-default over the whole tree. Unlike the other
primitives it isn't a per-rule check but a single, standing boundary a project
configures directly (one `.sloprail/file-guard/structure.yaml`).

→ [Configuring the structure gate](/guides/structure-gate)
