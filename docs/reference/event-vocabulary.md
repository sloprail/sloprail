---
title: Event vocabulary
description: The flat event kinds a guardrail can bind to, and how to get the exact set for your build.
kind: reference
related:
  - concepts/gate
  - reference/cli
  - reference/nature-shapes
---

A guardrail binds to **events** in its `on:` list. An event is a flat fact about
what is happening — a tool about to run, a file about to be written, a turn about
to end.

## The set is per-build — ask the load check

The exact event kinds available are whatever the modules compiled into *your*
build produce. The authoritative list for your build is what the **load check**
reports:

```bash
sr session start < /dev/null
```

It prints the event vocabulary and validates every declaration against it. Bind to
a kind your build doesn't produce and it names every kind that *is* available — so
a typo is caught at load, not silently ignored. Treat that output as the source of
truth; the kinds below are the common, stable ones.

## Common event kinds

| Kind | When it fires |
|---|---|
| `PreToolUse` | Before any tool runs — the general refusable moment. |
| `PreFileCreate` | Before a new file is written. |
| `PreFileUpdate` | Before an existing file is changed. |
| `PreFileWrite` | Alias the engine expands to `PreFileCreate` + `PreFileUpdate`. |
| `PostFileCreate` | After a file is created. |
| `PreCommand` / `PreCommandRun` | Before a command runs. |
| `PostTagWrite` | A [tag](/concepts/tag) was written in the trajectory (fires as the turn settles). |
| `Stop` | End of turn — the [gate](/concepts/gate)'s usual moment. |
| `SubagentStop` | End of a sub-agent's turn. |
| `SessionStart` | Session start — also the load check. |

## Aliases

Some kinds are aliases the engine expands, so one `match` covers several triggers.
`PreFileWrite` is the main one — write it once instead of duplicating the same
match across `PreFileCreate` and `PreFileUpdate`.

## Matching

Binding to a kind is the coarse filter. Narrow further with `match:` over the
event's fields and surrounding state — `event.path startsWith "memories/"`, or
`context["refactoring"].active`. See [Nature YAML shapes](/reference/nature-shapes)
for the expression surface.
