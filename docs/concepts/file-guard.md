---
title: file-guard
description: A rule bound to a file's state, re-checked every cycle until the file is right.
kind: explanation
sidebar:
  order: 1
related:
  - concepts/gate
  - concepts/context
  - reference/nature-shapes
  - guides/coding/refactor-regenerated
---

A **file-guard** is bound to a file's **state**, not to an event. A guarded file
must be fine, and it is re-checked every cycle until it is — the write that
triggered it is long gone, but the guard keeps holding.

This is the thing a bare hook cannot do. A hook checks once, at the moment of the
write, and forgets. A file-guard says "this property of this file must be true"
and stays responsible for it: touch the file and it is guarded again.

## Preventive: catch the loss before it happens

A file-guard can run **preventively** — at the *pre* moment, before the write
lands. That matters when what you're protecting would be gone afterward. The
content an edit would drop is still on disk at pre-time, so the check can compare
against it and refuse. A post-write check is too late; a file never committed
can't be restored.

## What it sees

A file-guard looks at the file: its path, and its [markers](/concepts/marker)
as a list. That's the coarse filter for *when* it applies — "any file under
`memories/` carrying an `invariant` marker." What it then *checks* is up to the
[checks](/reference/check-patterns) it runs.

## Reach for it when

The thing you're protecting is a property of a **file** — an invariant that must
keep holding, content that must not vanish, a moved block that must match its
origin.

For the exact YAML shape, see [Nature YAML shapes](/reference/nature-shapes).
