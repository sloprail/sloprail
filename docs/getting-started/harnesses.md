---
title: Harnesses
description: Which agent harnesses sloprail runs in, and how it plugs into each.
kind: reference
related:
  - getting-started/install
  - reference/event-vocabulary
---

sloprail is harness-agnostic in principle: it hooks the points a harness exposes —
before a tool runs, at turn-end — and turns what the harness reports into
[events](/reference/event-vocabulary) its rules bind to. What differs per harness
is only how it plugs in.

## Claude Code

Live. sloprail installs as a plugin:

```bash
/plugin install sloprail@sloprail-marketplace
```

The plugin registers the session hooks Claude Code calls, and ships its own
guardrails (and skills) alongside them. See [Install](/getting-started/install).

## Other harnesses

The engine's contract — hook points in, events out — is deliberately not specific
to one harness. Support for additional harnesses is added by wiring their hook
points to the same engine; the rules a project writes don't change.

Because the event vocabulary is per-build, the authoritative list of what a given
setup produces is always the load check:

```bash
sr session start < /dev/null
```
