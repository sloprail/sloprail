---
title: structure-gate
description: A deny-by-default allowlist over the file tree — the one thing a project configures.
kind: explanation
sidebar:
  order: 6
---

A **structure-gate** is a list of paths where writing is allowed; everything outside is denied. Deny-by-default over the tree.

Unlike the other primitives, it is a standing, **configured** boundary — one tree-wide allowlist a project sets up (`file-guard/structure.yaml`), not a per-rule check. When every place is disallowed, an agent that can't find a valid home is pushed to establish the structure first.

→ [Authoring: the file-guard nature](/guides/file-guard) covers where it lives.
