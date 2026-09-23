---
title: Tags
description: Labels the agent drops in its own messages that a rule reads from the trajectory.
kind: reference
related:
  - concepts/tag
  - concepts/context
  - reference/markers
---

A **tag** is a label the agent writes in its own message — `#refactor` — that a
rule reads from the [trajectory](/concepts/grounding). Where a
[marker](/reference/markers) sits on an artifact, a tag marks a *moment
in the run*: a declaration of intent or scope.

## How a tag is written

A tag is just text in the agent's message. Writing `#refactor scope=…` is the
declaration; there's nothing to install. When the turn settles, the engine
surfaces it as a `PostTagWrite` event, and a rule's script reads it out of the
trajectory.

## What reads a tag

A [context](/concepts/context) is the usual reader. Its `enter` runs on
`PostTagWrite`, finds the tag, and records what the agent committed to — a scope,
an intent — into the context's payload for a later [gate](/concepts/gate) to
check.

```yaml
on:
  - event: PostTagWrite
enter: ./enter.sh
```

The `enter` script reads the tag and its free-text (the `scope=…`) from the
trajectory — a tag counts only if it *actually appeared* in the run.

## Tag vs. marker

A tag is temporal (did it appear in the stream?); a marker is static (is it
present in the tree now?). They're two primitives split by host — see
[tag](/concepts/tag) and [marker](/concepts/marker) for why.
