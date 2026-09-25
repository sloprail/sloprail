---
title: Structure-gate
description: A deny-by-default allowlist over the file tree — the project's for the whole tree, a plugin's for the folders it owns.
kind: explanation
sidebar:
  order: 6
related:
  - guides/structure-gate
  - use-cases/long-runs/run-drifts-structure
  - concepts/marker
---

A **structure-gate** is a list of paths where writing is allowed; everything
outside is denied — deny-by-default. Unlike the other primitives it isn't a
per-rule check but a standing boundary: the project's own
`.sloprail/file-guard/structure.yaml` covers the whole tree, and an installed
plugin may ship one that covers only the folders it declares it owns (its
`scope`). They combine: inside a plugin's scope the plugin decides (the project
can still veto), everywhere else the project does.

→ [Configuring the structure gate](/guides/structure-gate)
