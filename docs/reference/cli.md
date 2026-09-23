---
title: CLI reference
description: Every sloprail command binary and its subcommands.
kind: reference
related:
  - guides/events
  - guides/file-guard
  - concepts/marker
---

There is one command to learn: **`sr`**. Everything is a subcommand of it —
`sr session start`, `sr file validate`, and so on.

Under the hood, `sr <group> <cmd>` is just a proxy for the `sr-<group>`
binary (`sr session start` runs `sr-session start`), passing through stdio,
signals, and exit codes unchanged. The plugin's hooks call those `sr-<group>`
binaries directly; you use `sr`. You rarely call any of this by hand in a
harness — the hooks run it for you — but this is the whole surface.

## `sr session`

The hook engine.

| Command | Purpose |
|---|---|
| `sr session start` | SessionStart hook. Also the **load check**: `sr session start < /dev/null` reports the event vocabulary and validates every declaration. |
| `sr session pre-tool` | PreToolUse hook — the refusable moment before a tool runs. |
| `sr session stop` | Stop hook — end of turn; the after-check moment. |
| `sr session subagent-stop` | SubagentStop hook. |
| `sr session state {get,set,list}` | The cross-cycle registry. `list --owner <ctx>` is the cross-guardrail read. |
| `sr session query` | Query the session transcript. |
| `sr session trajectory {describe,cite,tool-result,normalize}` | Inspect the trajectory — ground truth for "was a skill loaded", "did the action happen". |
| `sr session id` | The current session id. |

## `sr file`

Validate a file's frontmatter against a CUE schema.

```bash
sr file validate TASK.md --schema .sloprail/schemas/task.cue
sr file declarations .sloprail/          # list/validate declarations in a dir
```

## `sr mark`

Write or remove `// sr:<kind> <fqn>` markers in source. Markers anchor a
rule to a line and survive renames.

```bash
sr mark apply blueprint --user-service=src/users.ts:42
sr mark delete blueprint user-service
```

## `sr agent`

Run a judge harness-agnostically. This is what a `judge` check invokes.

```bash
sr agent --model size-md "<prompt>"
```

## Installation

```bash
go install ./services/...      # or:
make distribute-local          # rebuild into bin/, re-sign on macOS
```

Binaries find each other as siblings, then on `$PATH`. `SLOP_SUBBIN_DIR`
overrides the lookup (used by the e2e harness).

