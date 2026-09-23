---
title: marker
description: A durable label on a static artifact that anchors a rule to a position.
kind: explanation
sidebar:
  label: Marker
  order: 8
related:
  - guides/matchers
  - concepts/tag
---

A **marker** is a `// sr:<kind> <fqn>` label on a static artifact — a file, a line — that anchors a rule to a position and survives edits. Its question is "is this present in the tree now?"

Its counterpart is the [tag](/concepts/tag) — same idea, opposite host.

→ [Authoring: matchers](/guides/matchers)
