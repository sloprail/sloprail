---
title: marker
description: A durable label placed on a static artifact — a file, a line of code — that anchors a rule to a position.
kind: explanation
sidebar:
  order: 8
related:
  - concepts/tag
  - concepts/file-guard
  - reference/cli
---

A **marker** is a label placed **on a static artifact** — a file, a line of code:
`// sr:invariant foo`. It anchors a rule to a position in the artifact, and it
survives edits and renames so the rule keeps pointing at the right place.

A marker sits in content that **persists**. The check can re-read the file
whenever it wants; the question a marker answers is "is this marker present in the
tree right now?" That is what makes it the anchor for a
[file-guard](/concepts/file-guard), which reads a file's markers to decide whether
it applies.

## What a marker anchors

- An **invariant** on a function — the property that must keep holding.
- An **origin** on a moved block — `sr:moved-from <path>@<sha>:<lines>` — so a
  [reconciliation](/concepts/reconciliation) check can find the source the block
  must match.
- Any allow-by-label selection — the marker-based analogue of a
  [structure-gate](/concepts/structure-gate)'s path-based allow-list.

## Marker vs. tag

A marker's counterpart is the [tag](/concepts/tag). The split is by host: a marker
lives on a *static artifact*; a tag lives in the *trajectory*. One primitive for
the static half of the world, one for the temporal half. They are not
interchangeable settings of one thing — see the [tag](/concepts/tag) page for why.

Markers are written and removed with `sr mark`; see [Reference](/reference/cli).
